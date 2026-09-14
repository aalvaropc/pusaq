# Proposal: add-breaker-measurement

## Why

The circuit breaker exists and is unit-tested, but nothing reports when it opens or closes, its thresholds are compile-time defaults, and "detection and recovery times are measured, not asserted" (docs/design.md §5.6) has no measurement behind it. This change makes transitions observable, the parameters configurable, and the two times the output of one reproducible run against a provider that dies on command.

## What Changes

- Breaker transitions are observable: every change of state is reported to a hook, and the gateway logs one `breaker_state` line per transition with the backend, the previous state, and the new state.
- Breaker parameters come from configuration and are validated at startup: `PUSAQ_BREAKER_WINDOW`, `PUSAQ_BREAKER_MIN_SAMPLES`, `PUSAQ_BREAKER_THRESHOLD`, `PUSAQ_BREAKER_COOLDOWN`, `PUSAQ_BREAKER_PROBES`. The startup log states them.
- The mock provider gains a failure window: `POST /admin/fail?for=<duration>` makes every completion fail with 500 until the window ends (`DELETE /admin/fail` clears it); `GET /stats` reports `failing`; the mock logs the window's start and end instants.
- A breaker run: the load-test profile gains a fallback mock, `make breakertest` keeps three tenants under their limits through two replicas, flips the primary mock into failure at a known instant, and `loadtest/verify-breaker.sh` computes detection, open duration, recovery, probe count, extra attempts, and fallback-served requests per replica from the logs — failing if any client saw a 502 or detection exceeded its bound.
- `docs/measurements/breaker-detection.md` publishes the protocol and the numbers.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `resilience`: the breaker requirement gains configurable, validated parameters; observable transitions; and detection/recovery measured by one reproducible command.

## Impact

- Modified: `internal/breaker` (transition hook), `internal/config` (breaker variables), `cmd/pusaq` (wiring, logs), `internal/mockprovider` and `cmd/mockprovider` (failure window), `docker-compose.yml` (fallback mock, k6 env), `Makefile`, README, `docs/design.md` §5.6 and §8.
- New: `loadtest/breaker.js`, `loadtest/verify-breaker.sh`, `docs/measurements/breaker-detection.md`.
- No new dependencies.
