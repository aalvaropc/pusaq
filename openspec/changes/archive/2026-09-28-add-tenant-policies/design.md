# Design: add-tenant-policies

## Context

Tenants exist only as names attached to API keys. The completions flow resolves the alias before charging tokens and prices each completion in a finalizer; the request-limit stage and the token charge both fail open on store errors. Configuration is environment-only and validated at startup. See proposal.md.

## Goals / Non-Goals

**Goals:**

- Four policies a tenant operator actually reaches for first, from one variable, validated at startup like every other table.
- A denial that costs nothing: no provider call, no token charge, an event that says why.
- A budget that cuts traffic off, with the size of its overrun stated rather than hidden.

**Non-Goals:**

- Sensitive-data detection, PII redaction, injection heuristics; per-tenant rate limits; a control plane for policies — out of scope for this change.

## Decisions

1. **One record per tenant in one variable.** `tenant:key=value,...;tenant2:...` keeps the shape docs/design.md §4 describes ("the API key resolves the tenant and loads its policy") and the parsing idioms the other tables use. Tenants must be ones an API key can attribute — a policy nobody can trigger is a typo.

2. **The check lives in the completions flow, after alias resolution, before the token charge.** The policy needs the resolved alias (for `pusaq/auto`) and the parsed messages (for the prompt cap); a pipeline stage would parse the body a second time. Placing it before `chargeTokens` is what makes a denial free.

3. **Allowed models check the resolved alias and deny, never reroute.** Silently sending a disallowed `pusaq/auto` request to the default alias would make the routing table and `route_reason` lie; the denial names both aliases so the operator can fix either the rule or the policy.

4. **Prompt size in bytes of content, not tokens.** The tokenizer is an estimate (`chars/4`); a byte cap is exact, cheap, and what an operator can reason about. Role strings are excluded.

5. **Spend counter as a Lua `INCRBYFLOAT` + `EXPIRE`, read before admission, added in a finalizer.** The finalizer runs after the pricing finalizer (registration order), so it sees `req.Cost`, and it runs at stream end too. The read-then-add pair is not a reservation: the overrun is bounded by in-flight requests' cost, which is stated in the spec instead of solved with a heavier reservation scheme the budget does not need. The month is the UTC month of `ArrivedAt`, so the counter and the event stream agree.

6. **Statuses: 403, 402, 503.** OpenAI SDKs retry 408/409/429 and 5xx. A budget that resets next month must not be retried (402 with `Retry-After` at the month boundary); a policy denial is final (403); a fail-closed store outage is transient and should be retried with backoff (503).

7. **Fail-closed scope: the three decisions that consult the store before the provider is called.** The request limit, the token charge, and the budget read. Reconciliation runs after the fact and keeps its current behavior; rejecting there would help nobody.

8. **No per-request logs on denials.** The cost event already carries `error_code`; store outages still warn, as they do today, so `verify.sh`'s fail-open count keeps working.

## Risks / Trade-offs

- **One extra store round-trip** on budgeted tenants' requests (the read) plus one in the finalizer; both bounded by the same time budget as the limiter.
- **`Retry-After` up to a month** on 402 — honest, and the reason 429 was rejected.
- **A tenant name containing `:`, `;`, `,` or `=` cannot carry a policy**: startup error, never a silent misparse.
