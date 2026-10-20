# Proposal: fix-store-and-stream-timeouts

## Why

The limiter stages bound every store call with a 250 ms context, but the Redis client is built without `ContextTimeoutEnabled`, so a read in progress waits the client's default 5 s read timeout instead: against a store that accepts connections and never answers, one buffered request waits four times (request limit, budget read, token charge, spend update) — about 20 s — before failing open. On the provider side, the streaming client bounds only the response headers: dialing and the TLS handshake have no bound, a stream that goes silent after its first bytes holds the connection until the client gives up, and the HTTP server reaps neither slow request headers nor idle keep-alive connections.

## What Changes

- The Redis client honors context deadlines, so every store call ends within the limiter budget and the fail-open path takes about 250 ms per call.
- Streamed provider calls bound the dial, the TLS handshake, the response headers, and every silence between chunks by `PUSAQ_UPSTREAM_TIMEOUT`; a stream that stays silent longer ends as `upstream_error`.
- The HTTP server bounds request headers (10 s) and idle keep-alive connections (120 s); there is still no read or write timeout, so long SSE responses are unaffected.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `chat-completions`: provider calls are bounded end to end, streams included.

## Impact

- Modified: `cmd/pusaq/main.go`, `internal/provider/http.go`, `internal/ratelimit/lease.go` (comment), `docs/gateway.md`, `docs/configuration.md`, `docs/design.md`.
- New: `cmd/pusaq/main_test.go`.
- No new dependencies, no new configuration.
