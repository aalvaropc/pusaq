// Store-outage traffic (docs/measurements/redis-outage.md): one point per
// request with its tenant, status, error code, request id and send instant.

import http from 'k6/http';
import { sleep } from 'k6';
import { Counter, Trend } from 'k6/metrics';

const env = (name, fallback) => (__ENV[name] !== undefined && __ENV[name] !== '' ? __ENV[name] : fallback);

const BASE_URL = env('BASE_URL', 'http://lb:8080');
const RUN_ID = env('RUN_ID', 'manual');
const DURATION_S = Number(env('REDIS_DURATION', 90));
const TIMEOUT_S = 10;
const TENANTS = {
  open: { key: env('API_KEY_OPEN', 'redis-open'), rate: Number(env('REDIS_OPEN_RPS', 20)) },
  budget: { key: env('API_KEY_BUDGET', 'redis-budget'), rate: Number(env('REDIS_BUDGET_RPS', 5)) },
  closed: { key: env('API_KEY_CLOSED', 'redis-closed'), rate: Number(env('REDIS_CLOSED_RPS', 5)) },
};

const request = new Trend('redis_request', true);
const counters = {};
for (const name of Object.keys(TENANTS)) {
  counters[name] = {
    sent: new Counter(`${name}_sent`),
    ok: new Counter(`${name}_ok`),
    limited: new Counter(`${name}_limited`),
    store: new Counter(`${name}_store_unavailable`),
    server: new Counter(`${name}_other_5xx`),
    transport: new Counter(`${name}_transport`),
    other: new Counter(`${name}_other`),
  };
}

const anyStatus = http.expectedStatuses({ min: 100, max: 599 });

function scenario(name) {
  const rate = TENANTS[name].rate;
  return {
    executor: 'constant-arrival-rate',
    exec: name,
    rate,
    timeUnit: '1s',
    duration: `${DURATION_S}s`,
    preAllocatedVUs: Math.ceil(rate * TIMEOUT_S * 1.2) + 10,
    maxVUs: (Math.ceil(rate * TIMEOUT_S * 1.2) + 10) * 2,
    gracefulStop: `${TIMEOUT_S + 5}s`,
  };
}

export const options = {
  systemTags: ['status', 'scenario'],
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(95)', 'p(99)', 'count'],
  scenarios: { open: scenario('open'), budget: scenario('budget'), closed: scenario('closed') },
  thresholds: { dropped_iterations: ['count==0'] },
};

function errorCode(res) {
  try {
    return JSON.parse(res.body).error.code || '';
  } catch (_) {
    return '';
  }
}

function send(name) {
  const t = TENANTS[name];
  const c = counters[name];
  const sentAt = Date.now();
  const res = http.post(
    `${BASE_URL}/v1/chat/completions`,
    JSON.stringify({ model: 'pusaq/fast', messages: [{ role: 'user', content: 'ping' }], max_tokens: 8, metadata: { run_id: RUN_ID } }),
    {
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${t.key}` },
      responseCallback: anyStatus,
      responseType: 'text',
      timeout: `${TIMEOUT_S}s`,
    },
  );
  const code = res.status === 200 || res.status === 0 ? '' : errorCode(res);
  c.sent.add(1);
  if (res.status === 200) c.ok.add(1);
  else if (res.status === 429) c.limited.add(1);
  else if (code === 'store_unavailable') c.store.add(1);
  else if (res.status >= 500) c.server.add(1);
  else if (res.status === 0) c.transport.add(1);
  else c.other.add(1);
  request.add(res.timings.duration, {
    tenant: name,
    status: String(res.status),
    code: code || 'none',
    rid: res.headers['X-Pusaq-Request-Id'] || 'none',
    sent_ms: String(sentAt),
  });
}

export function open() {
  send('open');
}

export function budget() {
  send('budget');
}

export function closed() {
  send('closed');
}

export function setup() {
  for (let i = 0; i < 60; i++) {
    const res = http.get(`${BASE_URL}/readyz`, { responseCallback: anyStatus, timeout: '5s' });
    if (res.status === 200) return;
    sleep(1);
  }
  throw new Error(`${BASE_URL} never became ready`);
}

function count(data, name) {
  const m = data.metrics[name];
  return m && m.values ? m.values.count || 0 : 0;
}

export function handleSummary(data) {
  const client = {};
  for (const name of Object.keys(TENANTS)) {
    client[name] = {
      sent: count(data, `${name}_sent`),
      ok: count(data, `${name}_ok`),
      limited: count(data, `${name}_limited`),
      store_unavailable: count(data, `${name}_store_unavailable`),
      other_5xx: count(data, `${name}_other_5xx`),
      transport: count(data, `${name}_transport`),
      other: count(data, `${name}_other`),
    };
  }
  const summary = {
    run_id: RUN_ID,
    params: {
      duration_s: DURATION_S,
      timeout_s: TIMEOUT_S,
      open_rps: TENANTS.open.rate,
      budget_rps: TENANTS.budget.rate,
      closed_rps: TENANTS.closed.rate,
    },
    ended_at: new Date().toISOString(),
    client,
    dropped_iterations: count(data, 'dropped_iterations'),
    thresholds_passed: Object.values(data.metrics).every((m) => !m.thresholds || Object.values(m.thresholds).every((t) => t.ok)),
  };
  const lines = ['', `run ${RUN_ID}: open ${TENANTS.open.rate} rps, budget ${TENANTS.budget.rate} rps, closed ${TENANTS.closed.rate} rps for ${DURATION_S}s`];
  for (const [name, c] of Object.entries(client)) {
    lines.push(`  ${name}: sent=${c.sent} 200=${c.ok} 429=${c.limited} 503-store=${c.store_unavailable} other-5xx=${c.other_5xx} transport=${c.transport} other=${c.other}`);
  }
  lines.push(`  dropped=${summary.dropped_iterations} thresholds passed: ${summary.thresholds_passed}`, '');
  return {
    stdout: lines.join('\n'),
    [`out/${RUN_ID}-redis.json`]: JSON.stringify({ summary, k6: data }, null, 2),
  };
}
