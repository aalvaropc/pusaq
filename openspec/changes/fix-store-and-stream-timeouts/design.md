# Design: fix-store-and-stream-timeouts

## Context

Every Redis call already carries a deadline: the request-limit stage, the budget read, the token charge and the spend and reconciliation finalizers use `limiterBudget` (250 ms), and the lease janitor and `Close` use `sweepBudget`. go-redis v9 only applies a context deadline to socket reads and writes when `ContextTimeoutEnabled` is set; otherwise it waits `ReadTimeout` (5 s by default). The streaming provider client uses a zero-value transport with only `ResponseHeaderTimeout`. See proposal.md.

## Goals / Non-Goals

**Goals:**

- A hung store costs each call the limiter budget, not the client default.
- Every phase of a streamed provider call is bounded by the configured upstream timeout.
- The server sheds slow request headers and idle keep-alive connections.

**Non-Goals:**

- New configuration: the stream bounds reuse `PUSAQ_UPSTREAM_TIMEOUT`; the server bounds are constants.
- A read or write timeout on the server: it would cut long SSE responses.

## Decisions

1. **`ContextTimeoutEnabled` on the one Redis client.** The callers already set deadlines; enabling the option makes go-redis use them for reads and writes. Options are built by one function in `main` so a test checks the wiring against a TCP listener that never answers.

2. **One transport recipe for streams.** A clone of the default transport (proxy from the environment, HTTP/2) with the dial timeout, TLS handshake timeout and response-header timeout all set to the upstream timeout.

3. **Idle timeout as a body wrapper.** The stream body is wrapped in a reader whose timer restarts on every read that returns bytes; when it fires it cancels the request's context with a sentinel cause, and the wrapper returns that sentinel. It is never `context.Canceled`, so the stream ends as `upstream_error` rather than `stream_interrupted`. `Close` stops the timer and releases the context.

4. **Server timeouts as constants.** `ReadHeaderTimeout` 10 s and `IdleTimeout` 120 s; no `ReadTimeout` or `WriteTimeout`.

## Risks / Trade-offs

- A legitimate generation that pauses longer than the upstream timeout between two chunks ends as an upstream error. Accepted: the default is 30 s, and the timeout is configurable.
- A hung store still costs up to four limiter budgets on one request (about 1 s); fail-open stays the design.
