# Proposal: add-tenant-policies

## Why

Every tenant can use every model, send prompts of any size, and spend without limit; and when Redis is unreachable the limiter admits everyone, which is right for availability and wrong for a tenant with a hard budget. The design's policy filter (docs/design.md §4) is what turns cost accounting into cost control. This change builds its first four policies from one configuration variable.

## What Changes

- `PUSAQ_TENANT_POLICIES="tenant:key=value,...;tenant2:..."` with four fields per tenant: `models` (concrete aliases the tenant may be served by, `|`-separated), `max_prompt_chars` (bytes of message content), `budget_usd` (monthly spend ceiling), `fail_closed` (reject instead of admit when a limit's store is unreachable). Tenants without an entry are unrestricted. Every entry is validated at startup: unknown tenant, field, alias, or value; `pusaq/auto` in `models`; `budget_usd` without a price table or a store; `fail_closed` without a store.
- Enforcement in the completions flow after the alias is resolved and before any token is charged, so a denied request costs the tenant nothing: `pusaq/auto` resolving to a disallowed alias is denied and reported, never silently rerouted.
- Monthly spend per tenant in the store (`spend:{tenant}:YYYY-MM`, UTC, by the request's arrival time), added atomically from the priced cost in a finalizer — buffered responses and stream ends alike — and read before admission. The overrun is bounded by the cost of requests in flight; the counter is not a reservation. Cost events of budgeted tenants carry `spend_usd_month`.
- New normalized errors: `policy_denied` (403), `budget_exceeded` (402, `retry_after_ms` until the next UTC month), `store_unavailable` (503, fail-closed tenants only). Neither 402 nor 403 is retried by OpenAI SDKs; 503 is, with backoff, which suits a transient store outage.
- Fail-closed applies to the request limit, the token charge, and the budget read; token reconciliation is unchanged.

## Capabilities

### New Capabilities

- `tenant-policy`: the policy filter — allowed models, prompt cap, monthly budget with bounded overrun, fail-closed store behavior, startup validation.

### Modified Capabilities

- `rate-limiting`: store failure degrades open unless the tenant's policy is fail-closed.
- `cost-telemetry`: events of budgeted tenants record month-to-date spend.

## Impact

- New: `internal/spend` (monthly counter), `internal/gateway/policy.go`.
- Modified: `internal/core` (codes, `DevTenant`, spend field), `internal/config` (policies), `internal/gateway` (policy check, fail-closed in the request-limit stage and the token charge), `internal/telemetry`, `cmd/pusaq`, README, `docs/design.md` §4 and §9.
- No new dependencies. Not included: sensitive-data detection, PII redaction, injection heuristics.
