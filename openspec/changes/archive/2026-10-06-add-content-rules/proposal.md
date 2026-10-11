# Proposal: add-content-rules

## Why

The policy filter enforces allowed models, prompt size, budget, and fail-closed behavior, but it does not look at content: prompts with personal data or secrets, instruction-override phrasing, and time-dependent questions reach the semantic cache's shadow index and leave no trace on the cost event. The design says such prompts are never cached and that the policy filter's decisions are auditable per request (docs/design.md §4, §5.4, §5.7); the gateway also drops the client's `temperature` on the floor while the cache key is supposed to include it (constitution rule 2). This change closes those gaps with stated, measured heuristics — a policy filter, not a firewall.

## What Changes

- **`internal/policy`** (new, stdlib only): content rules that flag *kinds* of matches, never the matched text — sensitive data (`pii:email`, `pii:phone`, `pii:card` with Luhn, `pii:iban` with mod-97, `secret:api_key`, `secret:private_key`, `secret:jwt`, `secret:bearer`, `secret:assignment`), instruction-override phrasing in English and Spanish (`injection:ignore_instructions`, `injection:role_override`, `injection:prompt_leak`, `injection:role_marker`), and time-dependence cues (`temporal:now`, `temporal:relative_day`, `temporal:period`, `temporal:latest`, `temporal:clock`). Measured against a labeled corpus (`testdata/policy/content-rules.csv`, 176 rows EN/ES with hard negatives) with published precision/recall floors that the test suite enforces.
- **Policy filter**: after the prompt cap, every request's messages are classified by role (sensitive data anywhere; injection outside the system prompt; temporal cues in the user's words); the kinds go on the cost event as `policy_flags`; a tenant may turn a match into a 403 `policy_denied` (`sensitive=block`, `injection=block`; default `log`). A content denial never touches the store or the provider.
- **Cache eligibility**, decided once per request with a single recorded reason (`cache_skip`: `tenant_off`, `multi_turn`, `sensitive`, `injection`, `temporal`, `temperature`); the shadow cache offers only eligible requests. A tenant may opt out entirely (`cache=off`).
- **`temperature`** is accepted, forwarded upstream unchanged (a pointer, so an explicit 0 survives), and becomes part of the cache namespace (`temp=<value>` or `temp=none`); an explicit temperature above 0.7 makes the request ineligible. The namespace's policy version moves to `v1` so entries from the previous rules are never neighbors of new lookups.
- Docs: `docs/measurements/content-rules.md` (rules, limits, corpus, floors, numbers), README, `docs/design.md` §4 and §5.4.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `tenant-policy`: content rules with a per-tenant action; cache opt-out per tenant.
- `semantic-cache`: eligibility covers content and temperature, with one recorded reason; the namespace carries the real temperature and a policy version.
- `cost-telemetry`: events record the policy flags and the cache-eligibility reason.
- `chat-completions`: `temperature` is part of the accepted subset and is forwarded.

## Impact

- New: `internal/policy/`, `internal/gateway/cache.go`, `testdata/policy/content-rules.csv`, `docs/measurements/content-rules.md`.
- Modified: `internal/gateway/{policy,completions,stages}.go`, `internal/core/{request,cost,errors}.go`, `internal/telemetry/emitter.go`, `internal/provider/provider.go`, `internal/semcache/shadow.go`, `internal/config/config.go`, `cmd/pusaq/main.go`, README, `docs/design.md`.
- No new dependencies.
