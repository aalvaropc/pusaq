# Proposal: fix-upstream-error-classification

## Why

Every non-200 answer and every transport error from a provider becomes `upstream_error`, counts as a breaker failure, and moves the request to the next backend. A request the provider rejects as malformed (400, 413, 422) therefore opens the breakers of every backend after a few repetitions, so one tenant sending bad requests takes the gateway offline for everyone; the client sees 502 for its own mistake. A client that disconnects is treated the same way: the fallback continues with the canceled context and records failures against backends it never reached. A 200 answer whose body is an error object reaches the client as an empty 200, and a 200 body larger than the read cap is truncated into an unparseable one.

## What Changes

- A provider answer of 400, 413 or 422 is the client's error: the gateway responds 400 `invalid_request` without the provider's body, records a success for that backend's breaker (the backend is healthy), and tries no other backend.
- 401, 403, 404, 408, 429, 5xx, transport errors and timeouts stay `upstream_error` (502): breaker failure and fallback.
- When the client's request context ends, the chain stops: no breaker outcome is recorded, a half-open probe slot is released, no further backend is tried, and the cost event records the new code `client_canceled`.
- A 200 answer with an `error` object, without choices, or larger than the 4 MiB read cap is an upstream error.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `chat-completions`: provider rejections of the request are client errors; a canceled request is recorded as `client_canceled`.
- `resilience`: only backend failures feed a breaker as failures; cancellation stops the chain without an outcome.

## Impact

- Modified: `internal/provider/http.go`, `internal/gateway/completions.go`, `internal/breaker/breaker.go`, `internal/core/errors.go`, `docs/design.md`, `docs/gateway.md`.
- No new dependencies, no new configuration.
