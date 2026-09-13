// Breaker measurement: the primary upstream is made to fail at a known
// instant while three tenants keep sending traffic under their limits;
// detection (failure start → breaker open) and recovery (failure end →
// breaker closed) are read from the gateway and mock logs by
// verify-breaker.sh. This script's job is steady load, the flip, and the
// client-side view: no request may fail (fallback covers the outage).
//
// Runs inside the Compose `loadtest` profile; `make breakertest` drives it.

import http from 'k6/http';
import exec from 'k6/execution';
import { sleep } from 'k6';
import { Counter, Gauge } from 'k6/metrics';

const env = (name, fallback) => (__ENV[name] !== undefined && __ENV[name] !== '' ? __ENV[name] : fallback);

const BASE_URL = env('BASE_URL', 'http://lb:8080');
const MOCK_URL = env('MOCK_URL', 'http://mock-lt:9090');
const RPS = Number(env('BREAKER_RPS', 8)); // per tenant
const WARMUP = env('BREAKER_WARMUP', '20s');
const FAIL_FOR = env('BREAKER_FAIL_FOR', '5s');
const DURATION = env('BREAKER_DURATION', '45s');
const RUN_ID = env('RUN_ID', 'manual');
const KEYS = [env('API_KEY_A', 'lt-a'), env('API_KEY_B', 'lt-b'), env('API_KEY_TOLL', 'lt-c')];

const status200 = new Counter('status_200');
const status502 = new Counter('status_502');
const statusOther = new Counter('status_other');
const flipAt = new Gauge('flip_at_ms');

export const options = {
  discardResponseBodies: true,
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(95)', 'p(99)', 'count'],
  scenarios: {
    traffic: {
      executor: 'constant-arrival-rate',
      exec: 'traffic',
      rate: 3 * RPS,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: 20,
      maxVUs: 100,
      gracefulStop: '5s',
    },
    flip: {
      executor: 'shared-iterations',
      exec: 'flip',
      startTime: WARMUP,
      vus: 1,
      iterations: 1,
    },
  },
  thresholds: {
    // Fallback must cover the outage: the client never sees the failure.
    status_502: ['count==0'],
    status_other: ['count==0'],
    dropped_iterations: ['count==0'],
    'http_req_duration{scenario:traffic}': ['p(99)<1000'],
  },
};

const okOr502 = http.expectedStatuses(200, 502);

function waitReady(url) {
  for (let i = 0; i < 60; i++) {
    const res = http.get(`${url}/readyz`, { responseCallback: http.expectedStatuses(200, 502, 503) });
    if (res.status === 200) return;
    sleep(1);
  }
  throw new Error(`${url} never became ready`);
}

export function setup() {
  waitReady(BASE_URL);
  // Make sure no stale failure window is open from a previous run.
  http.del(`${MOCK_URL}/admin/fail`, null, { responseCallback: http.expectedStatuses(200) });
  return {};
}

export function traffic() {
  const key = KEYS[exec.scenario.iterationInTest % KEYS.length];
  const res = http.post(
    `${BASE_URL}/v1/chat/completions`,
    JSON.stringify({ model: 'pusaq/fast', messages: [{ role: 'user', content: 'ping' }], max_tokens: 8 }),
    { headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${key}` }, responseCallback: okOr502, timeout: '10s' },
  );
  if (res.status === 200) status200.add(1);
  else if (res.status === 502) status502.add(1);
  else statusOther.add(1);
}

export function flip() {
  const at = Date.now();
  const res = http.post(`${MOCK_URL}/admin/fail?for=${FAIL_FOR}`, null, {
    responseCallback: http.expectedStatuses(200),
    responseType: 'text',
  });
  flipAt.add(at);
  if (res.status !== 200) throw new Error(`flip failed: ${res.status} ${res.body}`);
  console.log(`flip: ${res.body.trim()}`);
}

function count(data, name) {
  const m = data.metrics[name];
  return m && m.values ? m.values.count || 0 : 0;
}

function pct(data, name) {
  const m = data.metrics[name];
  if (!m || !m.values) return null;
  const v = m.values;
  return { p50: v.med, p95: v['p(95)'], p99: v['p(99)'], max: v.max, requests: v.count };
}

export function handleSummary(data) {
  const flip = data.metrics.flip_at_ms ? data.metrics.flip_at_ms.values.value : null;
  const summary = {
    run_id: RUN_ID,
    params: { rps_per_tenant: RPS, tenants: KEYS.length, warmup: WARMUP, fail_for: FAIL_FOR, duration: DURATION },
    client: {
      status_200: count(data, 'status_200'),
      status_502: count(data, 'status_502'),
      status_other: count(data, 'status_other'),
      dropped_iterations: count(data, 'dropped_iterations'),
      flip_at: flip ? new Date(flip).toISOString() : null,
      latency_ms: pct(data, 'http_req_duration{scenario:traffic}'),
    },
    thresholds_passed: Object.values(data.metrics).every((m) => !m.thresholds || Object.values(m.thresholds).every((t) => t.ok)),
  };
  const l = summary.client.latency_ms || {};
  const lines = [
    '',
    `run ${RUN_ID}: ${KEYS.length} tenants x ${RPS} rps for ${DURATION}, primary fails for ${FAIL_FOR} at ${WARMUP}`,
    `  client: 200=${summary.client.status_200} 502=${summary.client.status_502} other=${summary.client.status_other} dropped=${summary.client.dropped_iterations} flip_at=${summary.client.flip_at}`,
    `  latency p50/p95/p99/max = ${l.p50}/${l.p95}/${l.p99}/${l.max} ms`,
    `  thresholds passed: ${summary.thresholds_passed}`,
    '',
  ];
  return {
    stdout: lines.join('\n'),
    [`out/${RUN_ID}-breaker.json`]: JSON.stringify({ summary, k6: data }, null, 2),
  };
}
