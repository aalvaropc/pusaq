# Tasks: add-token-limits

## 1. Core mechanics

- [x] 1.1 `internal/core`: `OnFinalize(fn)` on the envelope and `TokensCharged` on `CostEvent`; Accounting runs finalizers exactly once before emission — buffered, stream-end, and panic paths; tests prove exactly-once on all three.
- [x] 1.2 `internal/ratelimit`: `Adjust(ctx, tenant, delta)` reconcile script (refill, then `min(B, tokens+delta)`, TTL); miniredis tests: refund capped at capacity, extra charge goes negative, frozen-clock determinism.

## 2. Completions flow

- [x] 2.1 Charge after route resolution: input estimate (chars/4) + reservation (`max_tokens` | default); denial → 429 `rate_limited` with retry, provider never called; `TokensCharged` on the envelope/event.
- [x] 2.2 Reconciliation registered via `OnFinalize`: buffered success refunds unused reservation; error/panic refunds output reservation not consumed; stream reconciles at stream end with scanned usage; missing usage reconciles nothing. Tests with a fake token limiter recording Allow/Adjust calls and deltas.

## 3. Config, wiring, environment

- [x] 3.1 `internal/config`: `PUSAQ_TPM_LIMIT` (default 100000/min), `PUSAQ_TPM_BURST` (default = limit), `PUSAQ_TPM_RESERVE_DEFAULT` (default 512); validation + tests.
- [x] 3.2 `cmd/pusaq`: build the TPM limiter alongside RPM when Redis is configured; wire into the completions handler; compose sets a small TPM for observable limiting; README section + config table.

## 4. Verification and delivery

- [x] 4.1 Full suite `-race` green; lint + vet clean.
- [x] 4.2 E2E in the Compose network against real Redis: completions pass then 429 when the token budget drains; refund observable in Redis (bucket recovers by the unused reservation); cost events show `tokens_charged` next to actual usage.
- [x] 4.3 Conventional commit(s) on `feat/token-limits`, push, PR (stacked on `feat/rate-limiting`); CI green.
