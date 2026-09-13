# Proposal: add-feature-routing

## Why

`pusaq/auto` has been the alias that promises "you declare the intent, Pusaq picks the model" while quietly resolving like `pusaq/fast`. This change makes it a real, auditable decision: an explicit table from configuration maps the request's declared feature to a concrete alias, and every response says which rule fired.

## What Changes

- `pusaq/auto` resolves through routing rules keyed by the request's `metadata.feature_tag` (the OpenAI `metadata` map): `PUSAQ_ROUTING_RULES="<tag>=<alias>,...,default=<alias>"`, with the default table `classify`/`extract` → `pusaq/fast`, `reason`/`code` → `pusaq/best`, everything else → `pusaq/fast`. Targets are concrete aliases; a default entry is required; the table is replaced whole when configured.
- **BREAKING (configuration):** `pusaq/auto` is no longer a route. `PUSAQ_ROUTES` naming it is a startup error; the alias has no physical model of its own.
- Every completion response carries `X-Pusaq-Route-Reason` (`direct`, `rule:<tag>`, `default:untagged`, `default:unknown_tag`), including errors after resolution and streamed responses. Cost events record `feature_tag` (when sent, for any alias) and `route_reason`.
- A non-string `metadata.feature_tag` is rejected as `invalid_request`.
- The load-test profile sends toll traffic through `pusaq/auto` with a feature tag and fails the run on a wrong route reason; the README's Gemini example and the design doc's routing table are updated.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `chat-completions`: alias resolution — `pusaq/auto` resolves through feature-tag rules and reports its reason.
- `cost-telemetry`: events record the feature tag and routing reason.

## Impact

- Modified: `internal/config` (rules parsing, auto removed from routes), `internal/gateway` (resolution, header, request metadata), `internal/core` (fields), `internal/telemetry` (attrs), `cmd/pusaq` (wiring), `docker-compose.yml`, `loadtest/limiter.js`, `loadtest/verify.sh`, README, `docs/design.md` §5.5, `docs/measurements/limiter-exactness.md` (protocol note).
- No new dependencies.
