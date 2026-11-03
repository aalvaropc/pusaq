## Why

Four runtime behaviors disagree with the specifications or with the numbers they claim to produce. In half-open, any recorded outcome decides the breaker, including one from an attempt admitted before the breaker opened, and a release frees a probe slot even when the released attempt was no probe. The telemetry emitter can lose an event that races its shutdown without counting it, events of requests that exhausted the fallback chain omit `attempts`, a token charge skipped by fail-open looks like token limiting turned off, and `CostEvent.Latency` is a duration tagged `latency_ms`. The provider and embedding clients keep two idle connections per host, so 400 requests at concurrency 20 open over a hundred new connections. Calibration certifies τ* from a single predicted hit and writes undefined precision as 0, which weakens the precision floor of constitution rule 3.

## What Changes

- **BREAKING (internal API)** The breaker hands out a ticket per admitted attempt. Only outcomes of probes admitted in the current half-open episode close or reopen it; outcomes of attempts admitted in an earlier episode are ignored, and a release frees a probe slot only for a probe of the current episode. The gateway records and releases through the ticket on the buffered, fallback and streaming paths.
- The emitter delivers every event it accepted before `Close`; an event racing `Close` is delivered or counted as dropped, never lost.
- Every cost event carries `attempts`, zero when no backend was attempted.
- A cost event names each store-backed admission decision that failed open in `fail_open` (`rpm`, `tpm`, `budget`).
- `CostEvent` stores latency as integer milliseconds, so its JSON tag and the log sink agree.
- Provider (buffered and streaming) and embedding clients keep up to 256 idle connections per upstream host; redirects stay disabled and every timeout is unchanged.
- **BREAKING (calibration output)** τ* is the smallest τ whose one-sided Clopper–Pearson lower bound on precision, at a configurable confidence (default 0.95), meets the floor. `cmd/cachecal` gains `-confidence`; the curve reports predicted hits, false hits, precision, its lower bound, recall and F1, and writes undefined values as `undefined`. A failure says whether no τ reaches the floor or the best one lacks support, and how many predicted hits certification needs.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `resilience`: half-open is decided only by probes of the current episode; stale outcomes and releases are ignored.
- `cost-telemetry`: lossless shutdown race, `attempts` on every event, `fail_open` marking, latency in milliseconds in every encoding.
- `semantic-cache`: τ selection requires a confidence lower bound on precision; undefined precision is reported as undefined.

## Impact

- Code: `internal/breaker`, `internal/gateway` (completions flow, rate-limit and accounting stages, policy budget read), `internal/telemetry`, `internal/core` (cost event and request fields), `internal/provider`, `internal/embed`, `internal/calibrate`, `cmd/cachecal`.
- Telemetry consumers: `attempts` appears on every event; `fail_open` is a new field; `latency_ms` keeps its meaning.
- Calibration: the seed dataset (30 equivalent pairs) cannot certify τ* at 0.99 and 95% confidence; certification needs at least 299 predicted hits with no false hit.
- Docs: `docs/gateway.md`, `docs/design.md`, `docs/configuration.md`, `docs/operations.md`.
