# Tasks: fix-api-shape-and-lifecycle

## 1. Error shape and routing

- [ ] 1.1 `core`: `method_not_allowed` (405), `Error.Param`, `Error.Type()` with the design table, `InvalidField`; table test over every code.
- [ ] 1.2 Gateway router: 404 `not_found` and 405 `method_not_allowed` with `Allow`, JSON body and request ID; `writeError` renders `type`, `param` and a fresh request ID outside the pipeline; the stream error event shares the encoder.
- [ ] 1.3 `param` on every validation error; regression tests for 404/405, `type`/`param` per code and per validation field, and the stream event.

## 2. OpenAI response fields and authentication

- [ ] 2.1 `created` (provider value or gateway time) and `system_fingerprint` pass-through on buffered responses; tests for both cases.
- [ ] 2.2 Case-insensitive `Bearer` with space or tab separation on the API and the admin endpoint; table tests.

## 3. Readiness refusal

- [ ] 3.1 Not-ready check after authentication and before the body read; refusals carry a request ID and one `not_ready` cost event; a bad key gets 401 during a window; no request or token limit is charged.

## 4. Limits

- [ ] 4.1 Lua scripts expire buckets after the refill time with a one-minute floor; tests for a slow refill outliving idleness, a negative balance and the floor.
- [ ] 4.2 Per-call budget for lease refunds in sweeps and `Close`; test with several slow tenants.

## 5. Composition root and shutdown

- [ ] 5.1 Split `cmd/pusaq` into `newApp`, `serve` and `close` without behavior changes; a second signal during the drain exits immediately.
- [ ] 5.2 Tests: wiring from a `Config` (limiters in both modes, breakers and fallback, shadow cache caps, telemetry, policies, prices, routing), development-mode warning, server timeouts, SIGTERM drain with an in-flight request, second-signal exit in a child process; coverage before and after.

## 6. Docs and verification

- [ ] 6.1 `docs/gateway.md`, `docs/configuration.md`, `docs/operations.md`, `docs/design.md`.
- [ ] 6.2 Mutation check per new behavior; `make test`, `make lint`, `openspec validate --all`.
