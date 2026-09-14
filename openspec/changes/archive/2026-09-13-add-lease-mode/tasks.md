# Tasks: add-lease-mode

## 1. Limiter

- [x] 1.1 `internal/ratelimit`: `take` script and `Limiter.Take(ctx, tenant, need, want)`; the strict script unchanged. Test: a bucket holding 3.7 grants 3 whole tokens and then denies with retry.
- [x] 1.2 `internal/ratelimit`: `Leased` (`NewLeased`, `Allow`, `Close`, `Stats`) with per-tenant local tokens, lease expiry, denial caching, janitor refunds, final refund on `Close`. Tests on miniredis with a frozen clock: one acquisition per K requests; cumulative admissions equal the capacity with denials cached; expiry refunds (verified through a strict probe on the same key); denial cached until retry; `Close` returns held tokens; a failed refund is counted as lost.
- [x] 1.3 Randomized bound test (`math/rand/v2`, logged seed): admitted within `[min(demand,B) − M·K, min(demand,B)]` and conservation after `Close`; passes under `-race`.

## 2. Configuration and wiring

- [x] 2.1 `internal/config`: `PUSAQ_RATELIMIT_MODE` (`strict`|`lease`), `PUSAQ_LEASE_SIZE` (positive, at most the request burst), `PUSAQ_LEASE_TTL` (positive duration); tests for defaults, overrides, and each rejection.
- [x] 2.2 `cmd/pusaq`: wrap the request limiter in lease mode; startup log carries mode, lease size, TTL; `Close` with a stats log line in the shutdown sequence after the HTTP server drains.
- [x] 2.3 Harness: `LOADTEST_MODE`, `LOADTEST_LEASE_SIZE` in Makefile, Compose env, and k6 (bound floor lowered by replicas × lease size in lease mode; mode in the summary and stdout line).

## 3. Measurement and delivery

- [x] 3.1 `make loadtest LOADTEST_MODE=lease` (120 s and 300 s); add the lease rows to `docs/measurements/limiter-exactness.md` next to the strict ones: admitted vs bound, script calls per request, toll p50/p95/p99 both arms.
- [x] 3.2 README (rate-limiting section: modes and their contract; configuration list) and `docs/design.md` §5.2 (implemented semantics: refunds on TTL and drain, a crash loses at most K per tenant).
- [x] 3.3 Full suite `-race` green; lint + vet clean; conventional commit(s) on `feat/lease-mode`, PR stacked on `feat/limiter-load-test`; CI green.
