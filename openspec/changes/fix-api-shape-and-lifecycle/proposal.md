# Proposal: fix-api-shape-and-lifecycle

## Why

Several edges of the public API and of the process lifecycle contradict the specs or the OpenAI shape the gateway claims:

- Unknown paths and unsupported methods answer Go's plain-text `404 page not found` and `Method Not Allowed`, although every gateway error is specified to use one JSON shape.
- The `Bearer` scheme is matched case-sensitively and only with a space: `bearer`, `BEARER` and `Bearer<TAB>` get 401 "missing API key". Authentication schemes are case-insensitive (RFC 9110 §11.1). The admin endpoint has the same check.
- The buffered response lacks `created` and drops the provider's `system_fingerprint`; the error body lacks OpenAI's `type` and `param`.
- An injected not-ready window refuses requests before authentication and without a request ID, so a refusal cannot be correlated and a client with a bad key learns nothing about it until the window ends.
- Every rate-limit bucket expires after a fixed 10 minutes. A bucket whose refill takes longer than that (a low limit with a large burst, a negative token balance left by reconciliation) is reclaimed before it would have refilled and starts full: it admits more than the refill allows.
- A second SIGTERM during the drain is swallowed: the signal handler stays registered through the drain delay and the grace period.
- One 250 ms context covers a whole lease sweep, so one slow refund leaves every following tenant without budget and records their tokens as lost.
- `cmd/pusaq` has 3.9 % statement coverage: the wiring and the shutdown sequence live in one untested function.

## What Changes

- **Client-visible:** unknown paths answer 404 `not_found` and known paths with another method answer 405 `method_not_allowed` with an `Allow` header, both in the normalized JSON shape with a request ID.
- **Client-visible:** every error body adds `type` (an OpenAI-style category derived from the code) and `param` (the offending request field for validation errors, `null` otherwise). `code`, `message` and `retry_after_ms` are unchanged. The in-band stream error event carries the same fields. Every error response carries `X-Pusaq-Request-Id`; an error raised outside the pipeline gets a fresh ID.
- **Client-visible:** the buffered completion adds `created` (the provider's value, or the gateway's Unix time when the provider sends none) and passes `system_fingerprint` through when the provider sends one.
- **Client-visible:** the `Bearer` scheme is matched case-insensitively and may be separated from the key by spaces or tabs, on the API and on `POST /admin/readiness`.
- **Client-visible:** an injected not-ready window refuses requests after authentication and before the body is read or any limit is charged. A request with a missing or unknown key gets 401 during the window; an authenticated one gets 503 `not_ready` with a request ID and one cost event that records `not_ready` without a run ID.
- A bucket's store TTL is derived at every update from its balance and refill rate: the time the refill needs to fill the bucket, including the store-clock catch-up delay, with a one-minute floor. Reclaiming an idle bucket never admits more than the refill would.
- After the first SIGTERM or SIGINT the gateway unregisters its handler, so a second one terminates the process immediately.
- Each lease refund during a janitor sweep or `Close` has its own 250 ms budget.
- `cmd/pusaq` splits configuration wiring and the serve/drain sequence into unexported functions with tests for the wiring, the development-mode warning, server timeouts, the SIGTERM drain and the second-signal exit. Behavior is unchanged.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `request-pipeline`: one error shape with `type` and `param` for every error, including unknown routes and unsupported methods, always with a request ID.
- `chat-completions`: the buffered response carries `created` and the provider's `system_fingerprint`.
- `tenant-auth`: the `Bearer` scheme is case-insensitive and accepts spaces or tabs as separator.
- `operations`: not-ready refusals happen after authentication with a request ID and a cost event; a second termination signal exits immediately; the admin bearer check follows tenant authentication.
- `rate-limiting`: bucket expiry follows the refill time; lease refunds are budgeted per store call.

## Impact

- Code: `internal/core/errors.go`, `internal/gateway/{server,stages,completions}.go`, `internal/provider/provider.go` (two response fields), `internal/ratelimit/{ratelimit,lease}.go`, `internal/config/config.go`, `cmd/pusaq/main.go`.
- Tests: `internal/gateway`, `internal/ratelimit`, `internal/core`, `cmd/pusaq`.
- Docs: `docs/gateway.md`, `docs/configuration.md`, `docs/operations.md`, `docs/design.md`.
- No new configuration and no new dependencies.
