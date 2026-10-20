# Tasks: fix-store-and-stream-timeouts

## 1. Store calls

- [x] 1.1 Redis options built by one function with `ContextTimeoutEnabled`; a test against a TCP listener that never answers returns within twice the budget.
- [x] 1.2 Gateway test: a hung store behind the request limit, the token charge and the budget fails open and answers within 1.5 s.

## 2. Provider and server timeouts

- [x] 2.1 Streaming transport bounds dial, TLS handshake and response headers by the upstream timeout.
- [x] 2.2 Idle-timeout body wrapper; a silent stream ends as `upstream_error` through the gateway.
- [x] 2.3 Server `ReadHeaderTimeout` and `IdleTimeout`.

## 3. Docs and delivery

- [x] 3.1 `docs/gateway.md`, `docs/configuration.md`, `docs/design.md` §5.2.
- [x] 3.2 Suite with `-race`, lint, `openspec validate --all`; draft PR.
