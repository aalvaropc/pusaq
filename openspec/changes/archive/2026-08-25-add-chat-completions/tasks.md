# Tasks: add-chat-completions

## 1. Domain and provider abstraction

- [x] 1.1 `internal/core`: add `RequestedModel`/`ServedModel` to the request envelope, model/usage fields to `CostEvent`, and the `upstream_error` code (502); update accounting stage to copy them; verify existing tests still pass and new fields serialize.
- [x] 1.2 `internal/provider`: `Provider` interface with domain `ChatRequest`/`ChatResponse` types; `httpprovider` adapter with bounded client, non-2xx/timeout → `upstream_error`; table-driven tests against an httptest upstream (success, 500, timeout).

## 2. Gateway endpoint

- [x] 2.1 Completions types (request subset validation: model present, messages non-empty) with unit tests for valid/invalid payloads.
- [x] 2.2 Terminal handler: validate → resolve alias from routing table → call provider → map response, set `X-Pusaq-Model`, record models/usage on the envelope; unknown alias → `invalid_request`; tests cover round-trip, unknown model, malformed body, upstream 500 → 502, and cost-event fields.
- [x] 2.3 Route swap: remove `POST /v1/echo` and its tests; wire `POST /v1/chat/completions` through the pipeline; httptest end-to-end through `Server`.

## 3. Config and wiring

- [x] 3.1 `internal/config`: `PUSAQ_MOCK_URL` (default `http://localhost:9090`), `PUSAQ_UPSTREAM_TIMEOUT` (default `30s`), routing table defaults (`pusaq/fast|best|auto` → mock/`mock-model`); validation + tests.
- [x] 3.2 `cmd/pusaq`: build provider registry and routing table, wire the completions handler; verify `go build ./...`.

## 4. Verification and delivery

- [x] 4.1 Full suite: `go test -race -cover ./...` green, `golangci-lint` + `go vet` clean.
- [x] 4.2 E2E: mock provider running, gateway running, `curl POST /v1/chat/completions` with `pusaq/fast` returns the OpenAI-shaped body with `X-Pusaq-Model` and `X-Pusaq-Request-Id`; cost event line carries models and token usage; unknown model returns normalized 400.
- [x] 4.3 README API section + Makefile `smoke` updated to chat completions; verify commands as written.
- [x] 4.4 Conventional commit(s) on `feat/chat-completions`, push, PR to `main` (no co-authorship); CI green.
