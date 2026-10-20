// Limiter load test: exactness across two gateway replicas under sustained
// overload, co-tenant isolation, and the latency toll of the request limiter.
//
// Runs inside the Compose `loadtest` profile (see docker-compose.yml). All
// parameters come from the environment; `make loadtest` sets them.
//
// Scenarios (all constant arrival rate):
//   exactness      tenant A at RATE_MULT × its sustained rate for DURATION,
//                  against the limited replicas. Admitted must land inside
//                  the bucket bound (see bound() below); everything else 429.
//   neighbor       tenant B at 2 rps during the same window: zero 429s.
//   toll_limited   tenant C at TOLL_RPS (under its limit) after the overload,
//                  against the limited replicas, through pusaq/auto with a
//                  feature tag the routing rules send to pusaq/best.
//   toll_unlimited same, against replicas with limiting disabled. The two
//                  toll arms run in the same clock window; their p99 delta is
//                  the limiter's toll.
//
// Ground truth comes from three places: this script's 200/429 counters, the
// mock provider's per-model counter (snapshotted in setup/teardown), and the
// gateways' cost events (read by verify.sh).

import http from 'k6/http';
import { sleep } from 'k6';
import { Counter } from 'k6/metrics';

const env = (name, fallback) => (__ENV[name] !== undefined && __ENV[name] !== '' ? __ENV[name] : fallback);

const BASE_URL = env('BASE_URL', 'http://lb:8080');
const BASE_URL_NOLIMIT = env('BASE_URL_NOLIMIT', 'http://lb:8081');
const MOCK_URL = env('MOCK_URL', 'http://mock-lt:9090');
const RPM = Number(env('RPM', 600));
const BURST = Number(env('BURST', 100));
const DURATION = env('DURATION', '120s');
const RATE_MULT = Number(env('RATE_MULT', 10));
const TOLL_RPS = Number(env('TOLL_RPS', 8));
const TOLL_DURATION = env('TOLL_DURATION', '60s');
const RUN_ID = env('RUN_ID', 'manual');
const REPLICAS = Number(env('REPLICAS', 2));
const MODE = env('MODE', 'strict');
const LEASE_SIZE = Number(env('LEASE_SIZE', 10));
const KEYS = { A: env('API_KEY_A', 'lt-a'), B: env('API_KEY_B', 'lt-b'), TOLL: env('API_KEY_TOLL', 'lt-c') };

// Tolerance for refill accrued outside the nominal window (scenario ramp,
// scheduler jitter, the last in-flight requests). Two seconds of refill.
const EPSILON_S = 2;

// Models are the physical names the load-test routes map each alias to.
// The exactness tenant has a model of its own; neighbor and toll traffic
// share the other one (toll reaches it through pusaq/auto + feature tag).
const MODELS = { exactness: 'mock-exactness', other: 'mock-neighbor' };

function seconds(d) {
  const m = /^(\d+(?:\.\d+)?)(ms|s|m|h)$/.exec(String(d).trim());
  if (!m) throw new Error(`cannot parse duration ${d}`);
  const n = Number(m[1]);
  return { ms: n / 1000, s: n, m: n * 60, h: n * 3600 }[m[2]];
}

// Admitted-count bound for a bucket that starts full: capacity plus refill
// over the window, with ε seconds of tolerance on either side. In lease
// mode each replica may end the window holding up to LEASE_SIZE unspent
// tokens, so the lower bound drops by REPLICAS × LEASE_SIZE; the upper bound
// is the same — tokens are taken before they are spent.
function bound() {
  const r = RPM / 60;
  const T = seconds(DURATION);
  const held = MODE === 'lease' ? REPLICAS * LEASE_SIZE : 0;
  return {
    lower: Math.ceil(BURST + r * (T - EPSILON_S)) - held,
    upper: Math.floor(BURST + r * (T + EPSILON_S)),
    epsilon_s: EPSILON_S,
    rate_per_s: r,
    window_s: T,
    mode: MODE,
    lease_size: MODE === 'lease' ? LEASE_SIZE : 0,
  };
}

const B = bound();
const exactnessRate = RATE_MULT * (RPM / 60);
const tollStart = `${seconds(DURATION) + 10}s`;

// One counter per outcome per scenario; separate metric names avoid relying
// on sub-metric materialization for the summary.
const admittedExactness = new Counter('admitted_exactness');
const rejectedExactness = new Counter('rejected_exactness');
const otherExactness = new Counter('other_exactness');
const rejectedNeighbor = new Counter('rejected_neighbor');
const otherNeighbor = new Counter('other_neighbor');
const rejectedToll = new Counter('rejected_toll');
const mockServedExactness = new Counter('mock_served_exactness');
const mockServedOther = new Counter('mock_served_other');
const routeReasonMismatch = new Counter('route_reason_mismatch');

export const options = {
  discardResponseBodies: true,
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(95)', 'p(99)', 'count'],
  scenarios: {
    exactness: {
      executor: 'constant-arrival-rate',
      exec: 'exactness',
      rate: exactnessRate,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: 50,
      maxVUs: 400,
      gracefulStop: '5s',
    },
    neighbor: {
      executor: 'constant-arrival-rate',
      exec: 'neighbor',
      rate: 2,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: 5,
      maxVUs: 20,
      gracefulStop: '5s',
    },
    toll_limited: {
      executor: 'constant-arrival-rate',
      exec: 'tollLimited',
      startTime: tollStart,
      rate: TOLL_RPS,
      timeUnit: '1s',
      duration: TOLL_DURATION,
      preAllocatedVUs: 10,
      maxVUs: 50,
      gracefulStop: '5s',
    },
    toll_unlimited: {
      executor: 'constant-arrival-rate',
      exec: 'tollUnlimited',
      startTime: tollStart,
      rate: TOLL_RPS,
      timeUnit: '1s',
      duration: TOLL_DURATION,
      preAllocatedVUs: 10,
      maxVUs: 50,
      gracefulStop: '5s',
    },
  },
  thresholds: {
    // The claim under test: admitted lands inside the bucket bound.
    admitted_exactness: [`count<=${B.upper}`, `count>=${B.lower}`],
    other_exactness: ['count==0'],
    // Co-tenant isolation.
    rejected_neighbor: ['count==0'],
    other_neighbor: ['count==0'],
    // Toll tenants run under their limit: a 429 there is a setup error.
    rejected_toll: ['count==0'],
    // Toll traffic must be routed by its feature tag.
    route_reason_mismatch: ['count==0'],
    // The offered load must actually have been delivered.
    dropped_iterations: ['count==0'],
    // Referencing the per-scenario latency sub-metrics materializes them
    // for handleSummary; the bounds themselves are sanity checks only.
    'http_req_duration{scenario:toll_limited}': ['p(99)<1000'],
    'http_req_duration{scenario:toll_unlimited}': ['p(99)<1000'],
    'http_req_duration{scenario:neighbor}': ['p(99)<1000'],
    'http_req_duration{scenario:exactness}': ['p(99)<1000'],
  },
};

const okOr429 = http.expectedStatuses(200, 429);
const okOnly = http.expectedStatuses(200);

function completion(baseURL, key, alias, callback, metadata) {
  const body = { model: alias, messages: [{ role: 'user', content: 'ping' }], max_tokens: 8 };
  body.metadata = Object.assign({ run_id: RUN_ID }, metadata || {});
  return http.post(
    `${baseURL}/v1/chat/completions`,
    JSON.stringify(body),
    {
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${key}` },
      responseCallback: callback,
      timeout: '10s',
    },
  );
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
  // Bodies are discarded globally (options.discardResponseBodies); this
  // one request opts back in.
  const res = http.get(`${MOCK_URL}/stats`, { responseCallback: okOnly, responseType: 'text' });
  if (res.status !== 200) throw new Error(`mock /stats returned ${res.status}`);
  return JSON.parse(res.body).by_model || {};
}

export function setup() {
  waitReady(BASE_URL);
  waitReady(BASE_URL_NOLIMIT);
  return { statsBefore: mockStats() };
}

export function exactness() {
  const res = completion(BASE_URL, KEYS.A, 'pusaq/fast', okOr429);
  if (res.status === 200) admittedExactness.add(1);
  else if (res.status === 429) rejectedExactness.add(1);
  else otherExactness.add(1);
}

export function neighbor() {
  const res = completion(BASE_URL, KEYS.B, 'pusaq/best', okOr429);
  if (res.status === 429) rejectedNeighbor.add(1);
  else if (res.status !== 200) otherNeighbor.add(1);
}

const TOLL_METADATA = { feature_tag: 'toll' };

function checkRouted(res) {
  if (res.headers['X-Pusaq-Route-Reason'] !== 'rule:toll') routeReasonMismatch.add(1);
}

export function tollLimited() {
  const res = completion(BASE_URL, KEYS.TOLL, 'pusaq/auto', okOr429, TOLL_METADATA);
  if (res.status === 429) rejectedToll.add(1);
  checkRouted(res);
}

export function tollUnlimited() {
  checkRouted(completion(BASE_URL_NOLIMIT, KEYS.TOLL, 'pusaq/auto', okOnly, TOLL_METADATA));
}

export function teardown(data) {
  const after = mockStats();
  const delta = (model) => (after[model] || 0) - (data.statsBefore[model] || 0);
  mockServedExactness.add(delta(MODELS.exactness));
  mockServedOther.add(delta(MODELS.other));
}

function count(data, name) {
  const m = data.metrics[name];
  return m && m.values ? m.values.count || 0 : 0;
}

function pct(data, name) {
  const m = data.metrics[name];
  if (!m || !m.values) return null;
  const v = m.values;
  return { p50: v.med, p95: v['p(95)'], p99: v['p(99)'], avg: v.avg, max: v.max, requests: v.count };
}

function fmt(x) {
  return x == null ? 'n/a' : Number(x).toFixed(2);
}

export function handleSummary(data) {
  const admitted = count(data, 'admitted_exactness');
  const rejected = count(data, 'rejected_exactness');
  const other = count(data, 'other_exactness');
  const summary = {
    run_id: RUN_ID,
    params: {
      mode: MODE,
      lease_size: LEASE_SIZE,
      rpm: RPM,
      burst: BURST,
      duration: DURATION,
      rate_mult: RATE_MULT,
      offered_rps: exactnessRate,
      toll_rps: TOLL_RPS,
      toll_duration: TOLL_DURATION,
      replicas: REPLICAS,
    },
    bound: B,
    exactness: {
      admitted,
      rejected,
      other,
      total: admitted + rejected + other,
      mock_served: count(data, 'mock_served_exactness'),
      dropped_iterations: count(data, 'dropped_iterations'),
      within_bound: admitted >= B.lower && admitted <= B.upper,
    },
    neighbor: {
      rejected: count(data, 'rejected_neighbor'),
      other: count(data, 'other_neighbor'),
      latency_ms: pct(data, 'http_req_duration{scenario:neighbor}'),
    },
    // Neighbor and both toll arms share one physical model on the mock.
    mock_served_other: count(data, 'mock_served_other'),
    toll: {
      rejected: count(data, 'rejected_toll'),
      route_reason_mismatch: count(data, 'route_reason_mismatch'),
      limited_ms: pct(data, 'http_req_duration{scenario:toll_limited}'),
      unlimited_ms: pct(data, 'http_req_duration{scenario:toll_unlimited}'),
    },
    thresholds_passed: Object.values(data.metrics).every((m) => !m.thresholds || Object.values(m.thresholds).every((t) => t.ok)),
  };
  const tl = summary.toll.limited_ms || {};
  const tu = summary.toll.unlimited_ms || {};
  const lines = [
    '',
    `run ${RUN_ID} (${MODE}): RPM=${RPM} B=${BURST} T=${DURATION} offered=${exactnessRate} rps x${RATE_MULT}`,
    `  exactness: admitted=${admitted} bound=[${B.lower}, ${B.upper}] within=${summary.exactness.within_bound} rejected=${rejected} other=${other} mock_served=${summary.exactness.mock_served} dropped=${summary.exactness.dropped_iterations}`,
    `  neighbor:  rejected=${summary.neighbor.rejected} other=${summary.neighbor.other} | mock_served(neighbor + toll arms)=${summary.mock_served_other}`,
    `  toll:      limited p50/p95/p99 = ${fmt(tl.p50)}/${fmt(tl.p95)}/${fmt(tl.p99)} ms | unlimited = ${fmt(tu.p50)}/${fmt(tu.p95)}/${fmt(tu.p99)} ms | p99 delta = ${fmt(tl.p99 - tu.p99)} ms | rejected=${summary.toll.rejected} | route_reason_mismatch=${summary.toll.route_reason_mismatch}`,
    `  thresholds passed: ${summary.thresholds_passed}`,
    '',
  ];
  return {
    stdout: lines.join('\n'),
    [`out/${RUN_ID}.json`]: JSON.stringify({ summary, k6: data }, null, 2),
  };
}
