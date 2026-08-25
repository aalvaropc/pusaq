# Tasks: add-rate-limiting

## 1. Limiter

- [x] 1.1 `internal/ratelimit`: Lua token bucket (lazy refill, starts full, TTL, computed retry) + `Limiter.Allow(ctx, tenant, cost)` with injected clock; go.mod gains go-redis and miniredis (test).
- [x] 1.2 Tests on miniredis with frozen/advanced clock: burst-then-deny, refill restores and never exceeds B, tenant isolation, TTL set, retry_after math.
- [x] 1.3 **Exactness test**: two Limiter instances sharing one store, ≥100 concurrent goroutines, frozen clock, capacity B → exactly B admitted; run under `-race`.

## 2. Error contract and stage

- [x] 2.1 `internal/core`: `rate_limited` code (429) and `Error.RetryAfter`; renderer emits `retry_after_ms` and the `Retry-After` header; tests.
- [x] 2.2 Gateway stage after Auth: deny → `rate_limited` with retry; limiter error → fail-open + warning; tests: denial through pipeline (429 + header + cost event), fail-open on erroring limiter, disabled mode passthrough.

## 3. Config, wiring, environment

- [x] 3.1 `internal/config`: `PUSAQ_REDIS_ADDR` (unset = disabled), `PUSAQ_RPM_LIMIT` (default 60/min), `PUSAQ_RPM_BURST` (default = limit); validation + tests.
- [x] 3.2 `cmd/pusaq`: wire limiter + stage when enabled; startup warning when disabled.
- [x] 3.3 Compose: `pusaq` service (build target pusaq, Redis + mock by service name); README rate-limiting section + config table; Makefile unchanged targets still work.

## 4. Verification and delivery

- [x] 4.1 Full suite `-race` green; lint + vet clean.
- [x] 4.2 E2E against real Redis inside the Compose network: burst of B+N requests → exactly B×200 then 429s with `retry_after_ms` and `Retry-After`; `rate_limited` cost events; kill Redis mid-run → requests keep flowing (fail-open) with degradation logged.
- [x] 4.3 Conventional commit(s) on `feat/rate-limiting`, push, PR (stacked on `feat/tenant-auth`); CI green.
