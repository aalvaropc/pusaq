# Proposal: add-tpm-overrun-measurement

## Why

The token limit reserves on admission and reconciles when usage is known. Between those two moments the bucket is wrong by exactly the reservation error, and a tenant that under-reserves can consume more tokens than its bucket allows before reconciliation catches up. The design says the maximum observed overrun is reported as part of the exactness metric; nothing measures it yet. This change builds that run.

## What Changes

- A token-overrun run, `make tpmtest`: token limiting on, request limiting raised out of the way, the mock upstream answering with emulated latency so requests overlap in flight, and two tenants offering the same rate — one under-reserving (`max_tokens` below the mock's fixed usage), one over-reserving. `loadtest/tpm.js` drives the load; `loadtest/verify-tpm.sh` reads the gateways' cost events and computes, per arm, tokens charged vs consumed, the reservation error per request, the excess over the bucket bound at the end of the window and at its peak, and the largest number of requests in flight; it fails when consumption exceeds the bound, when the over-reserving arm shows any excess, or when the under-reserving arm's peak exceeds the stated ceiling.
- The load-test profile learns three knobs, all at their current defaults for the existing runs: `LOADTEST_TPM_LIMIT`, `LOADTEST_TPM_BURST`, and `LOADTEST_MOCK_LATENCY`.
- `PUSAQ_TPM_BURST` set to the empty string means "default", like the other optional variables, so a profile can declare it without choosing a value.
- `docs/measurements/tpm-overrun.md` publishes the formula, protocol, environment, results, and caveats; `docs/design.md` §5.3 is corrected to describe the reservation as it exists (`max_tokens` or the configured default; the charge stands when usage is unknown; the bucket, not the reservation, expires).

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `rate-limiting`: the token-limit requirement states the bound on consumption under overload and that it is measurable by one reproducible command.

## Impact

- New: `loadtest/tpm.js`, `loadtest/verify-tpm.sh`, `docs/measurements/tpm-overrun.md`.
- Modified: `internal/config` (empty burst), `docker-compose.yml`, `Makefile`, README, `docs/design.md` §5.3 and §8, `docs/measurements/limiter-exactness.md` (one line).
- No new dependencies; no gateway behavior change beyond the empty-burst default.
