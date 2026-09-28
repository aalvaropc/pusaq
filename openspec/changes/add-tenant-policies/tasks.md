# Tasks: add-tenant-policies

## 1. Core and spend

- [x] 1.1 `internal/core`: `policy_denied` (403), `budget_exceeded` (402), `store_unavailable` (503); `DevTenant`; `SpendUSDMonth` on the request and the event; status tests.
- [x] 1.2 `internal/spend`: monthly counter (`Key`, `NextMonth`, `Add` via atomic `INCRBYFLOAT`+`EXPIRE`, `Month`); tests on miniredis: accumulation, missing key is zero, UTC month key, TTL, next month across a year boundary.

## 2. Configuration

- [x] 2.1 `internal/config`: `TenantPolicy`, `PUSAQ_TENANT_POLICIES` parsing after `PUSAQ_API_KEYS`, cross-checks (known tenant, budget needs prices and a store, fail_closed needs a store), `HasBudgets`; tests for the accepted example, the dev tenant without keys, and every rejection.

## 3. Enforcement

- [x] 3.1 `internal/gateway`: `Policy`, `PolicyFor`, `SpendCounter`, `applyPolicy` between pricing and the token charge; fail-closed branches in `RateLimit` and `chargeTokens`; spend copied into both event paths; emitter attr `spend_usd_month`; `cmd/pusaq` wiring and startup logs. Tests: disallowed alias, auto resolved to a disallowed alias, prompt cap boundary, budget exhausted with retry, spend incremented after priced buffered and streamed completions (miniredis), unpriced and failed requests add nothing, fail-closed on the request limit, the token charge, and the budget read, tenants without the flag fail open.

## 4. Docs and delivery

- [x] 4.1 README (Tenants bullet, "Not implemented", error list, rate-limiting sentence, "Tenant policies" paragraph, configuration list) and `docs/design.md` §4 steps 1 and 3, §9 store row.
- [x] 4.2 Full suite `-race` green; lint + vet clean; `make up && make smoke` unchanged; manual checks of 403, 402 with `retry_after_ms`, and 503 under fail-closed; conventional commit(s) on `feat/tenant-policies`, draft PR.
