// Token-limit overrun measurement: with token limiting on and an upstream
// that takes LLM-like time to answer, how far can the tokens actually
// consumed exceed the bucket bound before reconciliation catches up?
//
// Two tenants, two arms, same offered rate:
//   under  tenant A, pusaq/fast → mock-exactness, max_tokens 8:
//          charged 2 + 8 = 10 on admission, actual usage 23 → reconciles −13.
//   over   tenant B, pusaq/best → mock-neighbor, max_tokens 64:
//          charged 2 + 64 = 66, actual 23 → refunds 43.
// verify-tpm.sh reads Σcharged, Σactual, and the peak excess over the
// bound from the gateways' cost events; this script drives the load and
// keeps the client-side counts.
//
// Runs inside the Compose `loadtest` profile; `make tpmtest` drives it.

import http from 'k6/http';
import { sleep } from 'k6';
import { Counter } from 'k6/metrics';

const env = (name, fallback) => (__ENV[name] !== undefined && __ENV[name] !== '' ? __ENV[name] : fallback);

const BASE_URL = env('BASE_URL', 'http://lb:8080');
const MOCK_URL = env('MOCK_URL', 'http://mock-lt:9090');
const TPM_LIMIT = Number(env('TPM_LIMIT', 6000));
const TPM_BURST = Number(env('TPM_BURST', TPM_LIMIT));
const RPS = Number(env('TPM_RPS', 20));
const DURATION = env('TPM_DURATION', '60s');
const RUN_ID = env('RUN_ID', 'manual');
const KEYS = { A: env('API_KEY_A', 'lt-a'), B: env('API_KEY_B', 'lt-b') };

const EPSILON_S = 2;
const ACTUAL_PER_REQUEST = 23; // the mock's fixed usage: 12 prompt + 11 completion
const PROMPT_ESTIMATE = 2; // ceil(len("user" + "ping") / 4)
const ARMS = {
  under: { key: KEYS.A, alias: 'pusaq/fast', model: 'mock-exactness', maxTokens: 8 },
  over: { key: KEYS.B, alias: 'pusaq/best', model: 'mock-neighbor', maxTokens: 64 },
};

function seconds(d) {
  const m = /^(\d+(?:\.\d+)?)(ms|s|m|h)$/.exec(String(d).trim());
  if (!m) throw new Error(`cannot parse duration ${d}`);
  const n = Number(m[1]);
  return { ms: n / 1000, s: n, m: n * 60, h: n * 3600 }[m[2]];
}

function bound() {
  const r = TPM_LIMIT / 60;
  const T = seconds(DURATION);
  const nominal = TPM_BURST + r * T;
  return {
    tokens_nominal: nominal,
    tokens_upper: TPM_BURST + r * (T + EPSILON_S),
    tokens_lower: TPM_BURST + r * (T - EPSILON_S),
    rate_per_s: r,
    burst: TPM_BURST,
    window_s: T,
    epsilon_s: EPSILON_S,
    actual_per_request: ACTUAL_PER_REQUEST,
    charged_under: PROMPT_ESTIMATE + ARMS.under.maxTokens,
    charged_over: PROMPT_ESTIMATE + ARMS.over.maxTokens,
    expected_admitted: Math.floor(nominal / ACTUAL_PER_REQUEST),
    // The under-reserving arm can drain the burst at the admission charge and
    // consume more: at most B × (actual − charged) / charged beyond the bound.
    peak_overrun_ceiling_under: Math.ceil((TPM_BURST * (ACTUAL_PER_REQUEST - (PROMPT_ESTIMATE + ARMS.under.maxTokens))) / (PROMPT_ESTIMATE + ARMS.under.maxTokens)),
  };
}

const B = bound();

const counters = {};
for (const arm of Object.keys(ARMS)) {
  counters[arm] = {
    admitted: new Counter(`admitted_${arm}`),
    rejected: new Counter(`rejected_${arm}`),
    other: new Counter(`other_${arm}`),
    mock: new Counter(`mock_served_${arm}`),
  };
}

function scenario(arm) {
  return {
    executor: 'constant-arrival-rate',
    exec: arm,
    rate: RPS,
    timeUnit: '1s',
    duration: DURATION,
    preAllocatedVUs: 50,
    maxVUs: 300,
    gracefulStop: '10s',
  };
}

export const options = {
  discardResponseBodies: true,
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(95)', 'p(99)', 'count'],
  scenarios: { under: scenario('under'), over: scenario('over') },
  thresholds: {
    other_under: ['count==0'],
    other_over: ['count==0'],
    dropped_iterations: ['count==0'],
    'http_req_duration{scenario:under}': ['p(99)<10000'],
    'http_req_duration{scenario:over}': ['p(99)<10000'],
  },
};

const okOr429 = http.expectedStatuses(200, 429);

function completion(arm) {
  const a = ARMS[arm];
  const res = http.post(
    `${BASE_URL}/v1/chat/completions`,
    JSON.stringify({ model: a.alias, messages: [{ role: 'user', content: 'ping' }], max_tokens: a.maxTokens }),
    { headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${a.key}` }, responseCallback: okOr429, timeout: '30s' },
  );
  if (res.status === 200) counters[arm].admitted.add(1);
  else if (res.status === 429) counters[arm].rejected.add(1);
  else counters[arm].other.add(1);
}

export function under() {
  completion('under');
}

export function over() {
  completion('over');
}

function waitReady(url) {
  for (let i = 0; i < 60; i++) {
    const res = http.get(`${url}/readyz`, { responseCallback: http.expectedStatuses(200, 502, 503) });
    if (res.status === 200) return;
    sleep(1);
  }
  throw new Error(`${url} never became ready`);
}

function mockStats() {
  const res = http.get(`${MOCK_URL}/stats`, { responseCallback: http.expectedStatuses(200), responseType: 'text' });
  if (res.status !== 200) throw new Error(`mock /stats returned ${res.status}`);
  return JSON.parse(res.body).by_model || {};
}

export function setup() {
  waitReady(BASE_URL);
  return { statsBefore: mockStats() };
}

export function teardown(data) {
  const after = mockStats();
  for (const arm of Object.keys(ARMS)) {
    const model = ARMS[arm].model;
    counters[arm].mock.add((after[model] || 0) - (data.statsBefore[model] || 0));
  }
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
  const arms = {};
  for (const arm of Object.keys(ARMS)) {
    arms[arm] = {
      tenant: ARMS[arm].key,
      alias: ARMS[arm].alias,
      max_tokens: ARMS[arm].maxTokens,
      charged_per_request: PROMPT_ESTIMATE + ARMS[arm].maxTokens,
      admitted: count(data, `admitted_${arm}`),
      rejected: count(data, `rejected_${arm}`),
      other: count(data, `other_${arm}`),
      mock_served: count(data, `mock_served_${arm}`),
      latency_ms: pct(data, `http_req_duration{scenario:${arm}}`),
    };
  }
  const summary = {
    run_id: RUN_ID,
    params: { tpm_limit: TPM_LIMIT, tpm_burst: TPM_BURST, rps_per_arm: RPS, duration: DURATION },
    bound: B,
    arms,
    dropped_iterations: count(data, 'dropped_iterations'),
    thresholds_passed: Object.values(data.metrics).every((m) => !m.thresholds || Object.values(m.thresholds).every((t) => t.ok)),
  };
  const lines = [
    '',
    `run ${RUN_ID}: TPM=${TPM_LIMIT}/min burst=${TPM_BURST} rps/arm=${RPS} T=${DURATION} → nominal ${B.tokens_nominal} tokens, upper ${B.tokens_upper}, expected admitted ≈ ${B.expected_admitted}/arm`,
    `  under (charge ${B.charged_under}, actual ${ACTUAL_PER_REQUEST}): admitted=${arms.under.admitted} rejected=${arms.under.rejected} other=${arms.under.other} mock_served=${arms.under.mock_served}`,
    `  over  (charge ${B.charged_over}, actual ${ACTUAL_PER_REQUEST}): admitted=${arms.over.admitted} rejected=${arms.over.rejected} other=${arms.over.other} mock_served=${arms.over.mock_served}`,
    `  dropped=${summary.dropped_iterations} thresholds passed: ${summary.thresholds_passed}`,
    '',
  ];
  return {
    stdout: lines.join('\n'),
    [`out/${RUN_ID}-tpm.json`]: JSON.stringify({ summary, k6: data }, null, 2),
  };
}
