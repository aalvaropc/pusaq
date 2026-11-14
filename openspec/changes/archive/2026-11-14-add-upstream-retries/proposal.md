## Why

A request tries each backend of its fallback chain at most once. A transient failure — a reset connection, a 503 from an overloaded upstream, a 429 with a `Retry-After` of half a second — sends the request to the next backend, or to a 502 when the chain is a single backend, although the same backend would likely answer after a short wait. Retrying is the usual remedy and also the usual way a struggling upstream gets buried: unbounded retries multiply the load exactly when the upstream can least absorb it. Every attempt also reaches the upstream with nothing that identifies it as the same request, so an upstream that deduplicates retries cannot recognize the gateway's.

## What Changes

- Same-backend retries for failures before any response byte reaches the client: a transport failure before the upstream answered (connection refused or reset, DNS or TLS failure, timeout before the response headers) and statuses 408, 429, 500, 502, 503 and 504. Rejections (400, 413, 422), every other upstream failure and a client that left are never retried. After the retries a failure allows, the request falls back as before.
- Each retry waits a capped exponential backoff with full jitter (200 ms base, 2 s cap) or the upstream's `Retry-After`, and starts only inside the request's retry window, measured from its first upstream attempt. A wait that would end outside the window sends the request to the next backend at once.
- Two bounds: a per-request maximum of retries across the chain (`PUSAQ_RETRY_MAX`, 0–5, default 0: retries off) and an instance-wide retry budget, a token bucket every request's first attempt credits with a ratio (`PUSAQ_RETRY_BUDGET_RATIO`, default 0.2) on top of a reserve of 10 retries refilled at one per second. `PUSAQ_RETRY_WINDOW` (default 10 s) sets the window.
- Every attempt, retries included, asks the backend's breaker for its own ticket and feeds it its own outcome; an open breaker stops retries on that backend.
- Streams retry only while opening, never after the stream started.
- Every upstream attempt carries `Idempotency-Key: pusaq-<request id>`, identical across the retries and fallbacks of one request. A client-sent `Idempotency-Key` is neither forwarded nor folded in.
- Cost events carry `retries` next to `attempts` and `retry_denied` (`budget`, `deadline`) when a retryable failure was not retried for one of those reasons.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `resilience`: bounded same-backend retries before fallback, the instance retry budget, retries through the breaker, streams retried only while opening.
- `chat-completions`: an idempotency key on every upstream attempt.
- `cost-telemetry`: `retries` and `retry_denied` on cost events.

## Impact

- Code: `internal/retry` (budget and backoff), `internal/provider` (retryable failure marking, `Retry-After`, idempotency header), `internal/gateway` (chain walker, accounting fields), `internal/core` (request and event fields), `internal/telemetry` (sink), `internal/config` and `cmd/pusaq` (three settings and the startup log).
- Behavior with default configuration: unchanged except the idempotency header. Retries are off by default, so the breaker-detection and regional-failover recipes keep the attempt counts and detection arithmetic their published numbers rest on.
- Telemetry consumers: `retries` appears on every event; `retry_denied` is a new optional field.
- Docs: `docs/gateway.md`, `docs/design.md`, `docs/configuration.md`.
