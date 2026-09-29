// Regional failover measurement: sustained traffic through the balancer
// while one region's readiness is failed on purpose at a known instant
// (docs/design.md §7, §8). Nothing here asserts a failover time — the run
// records every request with its region, attempt, and outcome, and
// verify-chaos.sh computes the numbers from those records plus the
// gateways' cost events.
//
// Arms:
//   main    non-streaming completions; retries once on a platform or
//           not-ready failure (what an SDK would do) — first-attempt
//           failures, recoveries by retry, and failures after retry are
//           counted separately;
//   stream  streamed completions long enough to be in flight at the flip;
//           complete / interrupted / refused are counted separately;
//   split   a tenant offered more than one region's share of its quota, to
//           see what a 50/50 split costs during failover;
//   probe   GET /readyz on each region's own URL and on the balancer, once
//           a second: an independent timeline of the failure;
//   flip    at WARMUP, fails region A's readiness for FAIL_FOR (unless
//           EXPERIMENT=none, the control run).
//
// `make chaos-run` drives it against the runtime module; `make chaos-local`
// against the Compose loadtest profile (plumbing check, no balancer health).

import http from 'k6/http';
import exec from 'k6/execution';
import { sleep } from 'k6';
import { Counter, Gauge, Trend } from 'k6/metrics';

const env = (name, fallback) => (__ENV[name] !== undefined && __ENV[name] !== '' ? __ENV[name] : fallback);

const BASE_URL = env('BASE_URL', 'http://lb:8080');
const GATEWAY_URLS = JSON.parse(env('GATEWAY_URLS', '{}')); // region → base URL
const ADMIN_TOKEN = env('ADMIN_TOKEN', '');
const EXPERIMENT = env('EXPERIMENT', 'region-fail'); // region-fail | none
const REGION_A = env('REGION_A', ''); // override; otherwise the warm-up majority
const QPS = Number(env('CHAOS_QPS', 8));
const STREAM_QPS = Number(env('CHAOS_STREAM_QPS', 2));
const SPLIT_QPS = Number(env('CHAOS_SPLIT_QPS', 15));
const WARMUP = env('CHAOS_WARMUP', '60s');
const FAIL_FOR = env('CHAOS_FAIL_FOR', '60s');
const DURATION = env('CHAOS_DURATION', '300s');
const RUN_ID = env('RUN_ID', 'manual');
const KEY_MAIN = env('API_KEY_MAIN', 'chaos-main');
const KEY_STREAM = env('API_KEY_STREAM', 'chaos-stream');
const KEY_SPLIT = env('API_KEY_SPLIT', 'chaos-split');
const RETRY_CAP_MS = 2000;
const RETRY_DEFAULT_MS = 500;

const REGIONS = Object.keys(GATEWAY_URLS);
if (REGIONS.length < 2) throw new Error('GATEWAY_URLS must map at least two regions to their URLs');

// One point per attempt: latency, tagged with what verify-chaos.sh needs.
const request = new Trend('chaos_request', true);
const probeTrend = new Trend('chaos_probe', true);
const startedAt = new Gauge('started_at_ms');
const flipAt = new Gauge('flip_at_ms');
const regionAIndex = new Gauge('region_a_index');
const mainFirstOK = new Counter('main_first_ok');
const mainFirstFailed = new Counter('main_first_failed');
const mainRetryOK = new Counter('main_retry_ok');
const mainRetryFailed = new Counter('main_retry_failed');
const streamComplete = new Counter('stream_complete');
const streamInterrupted = new Counter('stream_interrupted');
const streamRefused = new Counter('stream_refused');
const splitAdmitted = new Counter('split_admitted');
const splitLimited = new Counter('split_rate_limited');
const unexpected = new Counter('chaos_unexpected');

const anyStatus = http.expectedStatuses({ min: 100, max: 599 });

export const options = {
  systemTags: ['status', 'scenario'],
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(95)', 'p(99)', 'count'],
  scenarios: {
    main: {
      executor: 'constant-arrival-rate',
      exec: 'main',
      rate: QPS,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: 30,
      maxVUs: 200,
      gracefulStop: '15s',
    },
    stream: {
      executor: 'constant-arrival-rate',
      exec: 'stream',
      rate: STREAM_QPS,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: 20,
      maxVUs: 100,
      gracefulStop: '30s',
    },
    split: {
      executor: 'constant-arrival-rate',
      exec: 'split',
      rate: SPLIT_QPS,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: 30,
      maxVUs: 200,
      gracefulStop: '15s',
    },
    probe: {
      executor: 'constant-arrival-rate',
      exec: 'probe',
      rate: 1,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: 2,
      maxVUs: 5,
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
    // Bugs, not failover: malformed requests, bad keys, gateway panics.
    chaos_unexpected: ['count==0'],
    dropped_iterations: ['count==0'],
  },
};

function classify(res) {
  const region = res.headers['X-Pusaq-Region'] || '';
  if (res.status === 200) return { outcome: 'ok', region };
  if (res.status === 0) return { outcome: 'transport', region: '' };
  let code = '';
  try {
    code = JSON.parse(res.body).error.code;
  } catch (_) {
    code = '';
  }
  if (code === 'not_ready') return { outcome: 'not_ready', region };
  if (code === 'rate_limited') return { outcome: 'rate_limited', region };
  if (region === '' && (res.status === 429 || res.status >= 500)) return { outcome: 'platform', region: '' };
  if (res.status === 400 || res.status === 401 || res.status === 500) unexpected.add(1);
  return { outcome: 'error', region };
}

function retryDelayMs(res) {
  const ra = Number(res.headers['Retry-After']);
  if (!Number.isFinite(ra) || ra <= 0) return RETRY_DEFAULT_MS;
  return Math.min(ra * 1000, RETRY_CAP_MS);
}

function completion(key, body, attempt, arm) {
  const res = http.post(`${BASE_URL}/v1/chat/completions`, JSON.stringify(body), {
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${key}` },
    responseCallback: anyStatus,
    responseType: 'text',
    timeout: '10s',
  });
  const c = classify(res);
  request.add(res.timings.duration, {
    arm,
    attempt: String(attempt),
    status: String(res.status),
    region: c.region || 'none',
    outcome: c.outcome,
  });
  return { res, ...c };
}

const retriable = (o) => o === 'platform' || o === 'not_ready' || o === 'transport';

export function setup() {
  startedAt.add(Date.now());
  for (const [region, url] of Object.entries(GATEWAY_URLS)) {
    waitReady(url, region);
    if (ADMIN_TOKEN) clearWindow(url, region);
  }
  waitReady(BASE_URL, 'lb');

  let regionA = REGION_A;
  const share = {};
  if (!regionA) {
    // Which region does the balancer send this client to? Twenty warm
    // requests decide; the run also re-checks the pre-flip share afterwards.
    for (let i = 0; i < 20; i++) {
      const { region } = completion(KEY_MAIN, payload(8), 1, 'setup');
      share[region] = (share[region] || 0) + 1;
    }
    const [top, n] = Object.entries(share).sort((a, b) => b[1] - a[1])[0];
    if (!REGIONS.includes(top) || n < 18) {
      throw new Error(`no region takes >= 90% of warm-up traffic: ${JSON.stringify(share)}`);
    }
    regionA = top;
  }
  regionAIndex.add(REGIONS.indexOf(regionA));
  console.log(`region A = ${regionA} (warm-up share ${JSON.stringify(share)}); experiment ${EXPERIMENT}`);
  return { regionA };
}

function waitReady(url, label) {
  for (let i = 0; i < 60; i++) {
    const res = http.get(`${url}/readyz`, { responseCallback: anyStatus, timeout: '5s' });
    if (res.status === 200) return;
    sleep(1);
  }
  throw new Error(`${label} (${url}) never became ready`);
}

function clearWindow(url, region) {
  const res = http.post(`${url}/admin/readiness?state=up`, null, {
    headers: { Authorization: `Bearer ${ADMIN_TOKEN}` },
    responseCallback: anyStatus,
    responseType: 'text',
  });
  if (res.status !== 200) throw new Error(`clearing ${region}'s readiness window failed: ${res.status} ${res.body}`);
}

function payload(maxTokens, extra) {
  return Object.assign(
    { model: 'pusaq/fast', messages: [{ role: 'user', content: 'ping' }], max_tokens: maxTokens, metadata: { run_id: RUN_ID } },
    extra || {},
  );
}

export function main() {
  const first = completion(KEY_MAIN, payload(8), 1, 'main');
  if (first.outcome === 'ok') {
    mainFirstOK.add(1);
    return;
  }
  mainFirstFailed.add(1);
  if (!retriable(first.outcome)) return;
  sleep(retryDelayMs(first.res) / 1000);
  const second = completion(KEY_MAIN, payload(8), 2, 'main');
  if (second.outcome === 'ok') mainRetryOK.add(1);
  else mainRetryFailed.add(1);
}

function streamOnce(attempt) {
  const res = http.post(`${BASE_URL}/v1/chat/completions`, JSON.stringify(payload(32, { stream: true })), {
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${KEY_STREAM}` },
    responseCallback: anyStatus,
    responseType: 'text',
    timeout: '60s',
  });
  const c = classify(res);
  let outcome = c.outcome;
  if (res.status === 200) {
    outcome = res.body && res.body.trim().endsWith('data: [DONE]') ? 'complete' : 'interrupted';
  } else if (res.status === 0) {
    outcome = 'interrupted';
  } else if (outcome !== 'error') {
    outcome = 'refused';
  }
  request.add(res.timings.duration, { arm: 'stream', attempt: String(attempt), status: String(res.status), region: c.region || 'none', outcome });
  return { res, outcome, refusedAs: c.outcome };
}

export function stream() {
  const first = streamOnce(1);
  if (first.outcome === 'complete') return streamComplete.add(1);
  if (first.outcome === 'interrupted') return streamInterrupted.add(1);
  if (first.outcome !== 'refused' || !retriable(first.refusedAs)) return streamRefused.add(1);
  sleep(retryDelayMs(first.res) / 1000);
  const second = streamOnce(2);
  if (second.outcome === 'complete') streamComplete.add(1);
  else if (second.outcome === 'interrupted') streamInterrupted.add(1);
  else streamRefused.add(1);
}

export function split() {
  const c = completion(KEY_SPLIT, payload(8), 1, 'split');
  if (c.outcome === 'ok') splitAdmitted.add(1);
  else if (c.outcome === 'rate_limited') splitLimited.add(1);
}

export function probe() {
  const targets = Object.assign({ lb: BASE_URL }, GATEWAY_URLS);
  for (const [target, url] of Object.entries(targets)) {
    const res = http.get(`${url}/readyz`, { responseCallback: anyStatus, timeout: '5s' });
    probeTrend.add(res.timings.duration, {
      target,
      status: String(res.status),
      region: res.headers['X-Pusaq-Region'] || 'none',
    });
  }
}

export function flip(data) {
  if (EXPERIMENT === 'none') {
    console.log('control run: no injection');
    return;
  }
  if (!ADMIN_TOKEN) throw new Error('ADMIN_TOKEN is required to inject');
  const url = GATEWAY_URLS[data.regionA];
  const at = Date.now();
  const res = http.post(`${url}/admin/readiness?state=down&for=${FAIL_FOR}`, null, {
    headers: { Authorization: `Bearer ${ADMIN_TOKEN}` },
    responseCallback: anyStatus,
    responseType: 'text',
  });
  flipAt.add(at);
  if (res.status !== 200) throw new Error(`flip failed: ${res.status} ${res.body}`);
  console.log(`flip ${data.regionA}: ${res.body.trim()}`);
}

function count(data, name) {
  const m = data.metrics[name];
  return m && m.values ? m.values.count || 0 : 0;
}

function gauge(data, name) {
  const m = data.metrics[name];
  return m && m.values ? m.values.value : null;
}

export function handleSummary(data) {
  const flip = gauge(data, 'flip_at_ms');
  const started = gauge(data, 'started_at_ms');
  const summary = {
    run_id: RUN_ID,
    experiment: EXPERIMENT,
    params: { qps: QPS, stream_qps: STREAM_QPS, split_qps: SPLIT_QPS, warmup: WARMUP, fail_for: FAIL_FOR, duration: DURATION, regions: REGIONS },
    region_a: gauge(data, 'region_a_index') !== null ? REGIONS[gauge(data, 'region_a_index')] : null,
    started_at: started ? new Date(started).toISOString() : null,
    flip_at: flip ? new Date(flip).toISOString() : null,
    client: {
      main_first_ok: count(data, 'main_first_ok'),
      main_first_failed: count(data, 'main_first_failed'),
      main_retry_ok: count(data, 'main_retry_ok'),
      main_retry_failed: count(data, 'main_retry_failed'),
      stream_complete: count(data, 'stream_complete'),
      stream_interrupted: count(data, 'stream_interrupted'),
      stream_refused: count(data, 'stream_refused'),
      split_admitted: count(data, 'split_admitted'),
      split_rate_limited: count(data, 'split_rate_limited'),
      unexpected: count(data, 'chaos_unexpected'),
      dropped_iterations: count(data, 'dropped_iterations'),
    },
    thresholds_passed: Object.values(data.metrics).every((m) => !m.thresholds || Object.values(m.thresholds).every((t) => t.ok)),
  };
  const c = summary.client;
  const lines = [
    '',
    `run ${RUN_ID} (${EXPERIMENT}): main ${QPS} rps, stream ${STREAM_QPS} rps, split ${SPLIT_QPS} rps for ${DURATION}; region A ${summary.region_a}, flip at ${summary.flip_at}`,
    `  main:   first ok=${c.main_first_ok} failed=${c.main_first_failed} → retry ok=${c.main_retry_ok} failed=${c.main_retry_failed}`,
    `  stream: complete=${c.stream_complete} interrupted=${c.stream_interrupted} refused=${c.stream_refused}`,
    `  split:  admitted=${c.split_admitted} rate_limited=${c.split_rate_limited}`,
    `  unexpected=${c.unexpected} dropped=${c.dropped_iterations} thresholds passed: ${summary.thresholds_passed}`,
    '',
  ];
  return {
    stdout: lines.join('\n'),
    [`out/${RUN_ID}-chaos.json`]: JSON.stringify({ summary, k6: data }, null, 2),
  };
}
