# Tasks: add-feature-routing

## 1. Configuration

- [x] 1.1 `internal/config`: `pusaq/auto` removed from routes and rejected in `PUSAQ_ROUTES`; `PUSAQ_ROUTING_RULES` parsing (tag=alias, default required, concrete targets only, no duplicates) with the default table; tests for defaults, replacement, and each rejection.

## 2. Resolution and reporting

- [x] 2.1 `internal/gateway`: `RoutingRules.resolve`, `metadata.feature_tag` parsing, resolution before route lookup, `X-Pusaq-Route-Reason` on buffered, streamed, and error responses; `internal/core` and `internal/telemetry`: `feature_tag` and `route_reason` on the request, the event, and the log line; `cmd/pusaq` wiring and startup log. Tests: reason table, tagged auto reaches the rule's model, untagged and unknown tags use the default, direct alias records the tag, reason on 502, reason on streams, non-string tag rejected, auto without rules is an unknown model, emitter renders both fields.

## 3. Harness, docs, delivery

- [x] 3.1 Load-test profile: routes without `pusaq/auto`, rule `toll=pusaq/best`; toll scenarios send `pusaq/auto` with the tag and fail on a wrong reason; mock counter for the shared model; `verify.sh` line; protocol note in the measurements doc.
- [x] 3.2 README (API section, "Not implemented", configuration list, Gemini example) and `docs/design.md` §5.5.
- [x] 3.3 Full suite `-race` green; lint + vet clean; `make loadtest` passes with `route_reason_mismatch` 0; conventional commit(s) on `feat/feature-routing`, draft PR stacked on `feat/cost-pricing`.
