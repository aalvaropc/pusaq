# Measurement: settlement of unattempted token reservations

## Contract

A confirmed TPM reservation is fully refunded when finalization observes
zero provider attempts. An attempted call with unknown usage retains its
charge, including errors, timeouts, provider panics and abandoned streams.
Known usage reconciles normally. The finalizer makes one bounded adjustment
attempt independent of client cancellation and preserves the original request
outcome if that adjustment fails. `tokens_charged` remains the initial
reservation, not a net-consumption field.

## Deterministic verification, 2026-10-20

The gateway tests run the complete accounting pipeline. The ledger test
executes the actual Lua scripts against miniredis with frozen store time:

| Case | Observed result |
|---|---|
| All breakers open, buffered or streaming request | 0 provider calls; one reservation of 35 and one refund of 35; original 502 retained |
| Cancellation after reservation, before invocation | One full refund with a live independent context bounded by 250 ms; original 499 retained |
| Refund fails | One adjustment attempt and reconciliation warning; original 502 and one cost event retained |
| Reservation denied or store fails open | No refund of an unconfirmed charge |
| Attempted provider error, timeout or panic, buffered or streaming | Reservation retained while consumption is unknown |
| Primary fails and fallback reports 5 tokens | One charge of 35 and one adjustment of 30, with two provider attempts |
| 100 unattempted requests, frozen store time, capacity 1000 | 100 original 502 outcomes, 0 provider calls, final balance exactly 1000 |

Before the correction, the zero-attempt regressions fail: no refund is made
and the ledger reaches 429 at request 29 after 28 reservations consumed
980 tokens. After the correction the regressions pass with the race detector.
Existing usage reconciliation and abandoned-stream tests also pass.

## Local TPM run, 2026-10-20

Command: `make tpmtest RUN_ID=refund-tpm-20261020`.

Compose runs two gateway replicas, Redis 7, k6 and mock providers on one
host. TPM is 6000/min, burst 1000; each arm offers 20 requests/s for 60 s.
The mock takes 2 s and consumes 23 tokens per response. The initial charge
is 10 tokens for the under-reserving arm and 66 for the over-reserving arm.

| Arm | Admissions (client = gateway = mock) | Rejections | Actual tokens | Peak overrun | End overrun |
|---|---|---|---|---|---|
| Under | 313 | 888 | 7199 | 480.91 | 0 |
| Over | 289 | 912 | 6647 | 0 | 0 |

The verifier's upper bound is 7200 tokens over the window. The observed
peak remains below the bound from reservation error and concurrent requests.
There are zero reconciliation failures, fail-open admissions, unexpected
outcomes or dropped iterations. Gateway events are drained before verification.
This run checks ordinary usage reconciliation; the frozen-store ledger
above isolates the zero-attempt correction. No cloud or real provider is used.
