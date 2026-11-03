const UNIT_SECONDS = { ns: 1e-9, us: 1e-6, 'µs': 1e-6, 'μs': 1e-6, ms: 1e-3, s: 1, m: 60, h: 3600 };
const TERM = '(\\d+\\.?\\d*|\\.\\d+)(ns|us|µs|μs|ms|s|m|h)';
const DURATION = new RegExp(`^(${TERM})+$`);

export function durationSeconds(text) {
  const s = String(text);
  if (s === '0') return 0;
  if (!DURATION.test(s)) throw new Error(`not a Go duration (e.g. 500ms, 10s, 1m30s): ${s}`);
  const term = new RegExp(TERM, 'g');
  let total = 0;
  let m;
  while ((m = term.exec(s)) !== null) total += Number(m[1]) * UNIT_SECONDS[m[2]];
  return total;
}

export const retriable = (outcome) => outcome === 'platform' || outcome === 'not_ready' || outcome === 'transport';

export function streamOutcome(res, classified) {
  if (res.status === 200) return res.body && res.body.trim().endsWith('data: [DONE]') ? 'complete' : 'interrupted';
  if (res.status === 0) return res.timings && res.timings.receiving > 0 ? 'interrupted' : 'never_started';
  return classified === 'error' ? 'error' : 'refused';
}

export const retryStream = (outcome, classified) => (outcome === 'never_started' || outcome === 'refused') && retriable(classified);
