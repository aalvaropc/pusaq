## Why

Every store call on the request path is bounded by the 250 ms limiter budget, but nothing remembers that the store is failing. With Redis paused under load (`make chaos-redis`, hung arm), every decision waits its full budget before failing open: the fail-open tenant's p50 goes from 2–6 ms to about 505 ms and the budgeted tenant's to about 1005 ms for the whole outage, with 902–904 deadline-bound store failures per 30 s window. In the down arm the Redis client's own dial circuit cuts the waits after 7.4–11.8 s; a hung store has no such cut, and recovery after a stop depends on that client's probe schedule (0.009–4.859 s per replica).

## What Changes

- Each gateway instance guards its store client with a store health gate built on the existing circuit breaker (`internal/breaker`): store calls the store did not answer (timeouts, connection and dial errors) are failures; over the last 20 calls, with at least 10 of them, a failure rate above one half opens the gate.
- While the gate is open, every store call — request-limit admission (strict and lease), token charge and reconciliation, budget read and spend increment, lease acquisition and refunds — fails at once without contacting the store. Callers handle that failure exactly as a timed-out call today: fail-open tenants are admitted with `fail_open` on the cost event, fail-closed tenants get 503 `store_unavailable`, and skipped reconciliations, increments and refunds are logged or counted as lost.
- One second after opening, the first store call takes the gate's single probe slot for a background `PING` bounded by the same 250 ms; that call and the calls during the probe are skipped. An answer closes the gate; anything else reopens it for another second.
- Every gate transition is a `breaker_state` log record with `component` `store`. The event report shows store-gate transitions in its rate-limiting view; the store-outage harness collects them per replica and the verifier reports detection and recovery per replica.
- No new configuration: the rule, the cooldown and the probe budget are constants. A healthy store sees exactly the calls it saw before; the gate adds no round trip.
- The store client opens a connection in one attempt of at most 250 ms: go-redis dials on its own context, up to five attempts of up to 5 s each by default, which let dials and name lookups begun during an outage outlive the probes that follow the restore.
- `docs/measurements/redis-outage.md` gains a dated section with both arms measured on the gated gateway next to the published runs; the chaos report's store-outage case states the fix and its verification.

## Capabilities

### New Capabilities

### Modified Capabilities

- `rate-limiting`: a store failure degrades open, and an instance store health gate skips the store while it keeps failing, probing it once per cooldown.
- `tenant-policy`: fail-closed tenants are refused at once while the gate is open.
- `resilience`: the store-outage measurement reports the gate's detection and recovery per replica and fails on gate records that contradict the window.
- `operations`: the event report's rate-limiting view lists store-gate transitions.

## Impact

New package `internal/storegate`; `internal/breaker` (tickets say whether they are half-open probes); wiring, startup log and the store dial timeout in `cmd/pusaq`; `internal/report` and its template; `loadtest/redis-outage.sh`, `loadtest/verify-redis.sh` and their fixtures; `docs/design.md`, `docs/operations.md`, `docs/measurements/redis-outage.md` and `docs/measurements/chaos-report.md`. Request handling in `internal/gateway`, `internal/ratelimit` and `internal/spend` is unchanged: the gate sits in the store client they share.
