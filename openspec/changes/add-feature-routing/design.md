# Design: add-feature-routing

## Context

Alias resolution is a map lookup in the completions handler; `pusaq/auto` was a third key pointing at the same model as `pusaq/fast`. Configuration is environment-only and rejects unknown aliases at startup. The cost event already carries requested and served model. See proposal.md.

## Goals / Non-Goals

**Goals:**

- An auditable decision: a table anyone can read, and a reason on every response and event.
- No physical model names in code; `pusaq/auto` stops pretending to have one.
- Rules validated at startup, like every other configuration table.

**Non-Goals:**

- A learned router, cascades, or an LLM judge in the request path (docs/design.md §5.5); per-tenant rule tables (a policy concern); a feature tag from headers.

## Decisions

1. **Feature tag from `metadata.feature_tag`.** The OpenAI request accepts a `metadata` map and SDKs pass it through untouched, so a client tags requests without leaving the API subset. Absent means untagged; a non-string value is a client error rather than a silent default, because a typo in the caller's code should not quietly change which model is billed.

2. **Rules resolve to concrete aliases, never to physical models.** `PUSAQ_ROUTES` remains the only place a physical name appears; the rules layer maps intent to alias, and the alias layer maps alias to model. Two tables, two questions, each auditable on its own. `pusaq/auto` in `PUSAQ_ROUTES` is therefore an error: the alias would otherwise have two conflicting definitions.

3. **Replace, do not merge.** A configured `PUSAQ_ROUTING_RULES` is the whole table. Merging with defaults would let a stale default rule survive a deliberate rewrite; requiring `default=` keeps the fallback explicit.

4. **Reason vocabulary is fixed and small.** `direct`, `rule:<tag>`, `default:untagged`, `default:unknown_tag` distinguish the four cases an operator would query: which traffic never used the router, which rule fired, and whether a default was hit because clients do not tag or because they tag things nobody expected. The same string lands on the response header and the cost event.

5. **The tag is recorded for every alias.** Cost per feature (docs/design.md §5.7) needs the tag even when the client chose the model explicitly; `feature_tag` on the event is independent of routing.

6. **The load test keeps its isolation.** With `pusaq/auto` no longer a route, only two physical models exist in the load-test profile. The overload tenant keeps a model of its own for the exactness cross-check; neighbor and toll traffic share the second. Toll traffic goes through `pusaq/auto` with a feature tag and a rule, so the router is exercised under load and a wrong reason fails the run.

## Risks / Trade-offs

- **Configuration break for `pusaq/auto=` routes.** Deliberate and loud: the startup error names the fix. The README example and the Compose profile are updated in the same change.
- **A zero-value rule set makes `pusaq/auto` an unknown model.** Only reachable when the handler is built without rules (tests); the composition root always supplies the default table.
