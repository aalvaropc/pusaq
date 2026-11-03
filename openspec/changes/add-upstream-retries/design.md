## Context

`viaChain` (`internal/gateway/completions.go`) walks the route's backends in order: it skips a backend whose breaker is open, makes one attempt per backend, feeds each failed attempt to that backend's breaker through the attempt's ticket, and returns the first answer. A provider rejection (400, 413, 422) ends the chain as `invalid_request`; a client that leaves ends it as `client_canceled`. The provider adapter (`internal/provider/http.go`) maps every non-200 status other than a rejection, and every transport error, to the same `upstream_error`; it keeps no trace of the status or of a `Retry-After` header. The token charge happens once before the chain and is reconciled once in a finalizer. Upstream requests are built from scratch, so no client header reaches an upstream.

## Goals / Non-Goals

**Goals:**
- Recover transient upstream failures on the same backend before falling back.
- Bound retry load per request and per instance, with a bound that can be stated as a formula and checked in a test.
- Keep the breaker's view per attempt, and keep streams unrestarted once they started.
- Let an upstream that deduplicates by idempotency key recognize the retries of one request.
- Keep the published breaker and failover recipes reproducible with default configuration.

**Non-Goals:**
- Retries in the shadow cache's embedder.
- Hedged or parallel attempts.
- A budget shared across instances through Redis.
- Retrying a stream after it started, or a buffered call whose answer arrived and failed to parse.
- Provider-specific hints beyond the standard `Retry-After` header.

## Decisions

1. **The provider classifies, the gateway decides.** The adapter wraps a retryable failure in `*provider.RetryableError` (status, parsed `Retry-After`, cause) inside the usual `upstream_error`, so callers that ignore it see no change. Retryable: an error from the HTTP client before a response exists (connection refused or reset, DNS, TLS, a timeout before the response headers), and statuses 408, 429, 500, 502, 503, 504. Not retryable: other statuses (401, 403 and 404 are configuration errors a retry does not heal; redirects are refused by policy; 501 and 505 are permanent), a 200 without a completion, and a buffered body that fails to read after the headers arrived — at that point the upstream generated, and likely billed, the completion. `Retry-After` is read as delta-seconds or an HTTP date, clamped to [0, 24 h]; anything else is absent. The gateway's walker owns the policy: whether to retry, how long to wait, and the bounds.

2. **One per-request maximum across the chain.** `PUSAQ_RETRY_MAX` counts retries for the whole request, not per backend, so a request makes at most `backends + max` attempts. Validated to 0–5; 0 turns retries off and leaves the walker's behavior exactly as before.

3. **Capped exponential backoff with full jitter.** Before the n-th retry of one backend the walker waits `u · min(2 s, 200 ms · 2^(n−1))` with `u` uniform in [0, 1): full jitter spreads requests that failed at the same instant, such as a burst of 503s, across the whole interval, which keeps the retry wave from reforming. The exponent restarts on each backend. When the upstream sends a positive `Retry-After`, that value replaces the computed delay. Base and cap are constants: a gateway with a fallback prefers moving on over long waits, and the window below bounds the total.

4. **A retry window measured from the first attempt.** A retry starts only if its wait ends before the request's retry deadline: `PUSAQ_RETRY_WINDOW` (default 10 s, at most 10 min) after the request's first upstream attempt, or the request context's deadline when that is earlier. A `Retry-After` that does not fit sends the request to the next backend at once instead of waiting. With the default 30 s upstream timeout and a 10 s window, an attempt that timed out leaves no room for a retry: a timed-out buffered call usually means the upstream generated a completion it bills, so retrying it doubles spend at an upstream that ignores the idempotency key. An operator who wants timeouts retried sets the window above the upstream timeout.

5. **An instance retry budget, Finagle/Envoy style.** One token bucket per instance, shared by all requests: each request's first upstream attempt credits it with `PUSAQ_RETRY_BUDGET_RATIO` (default 0.2, in (0, 1]); it also refills at one retry per second; it holds at most 10 retries; each retry withdraws one. Over any interval of length T with N requests reaching an upstream, retries ≤ 10 + T·1/s + ratio·N, so a failing upstream receives at most (1 + ratio) times its first-attempt load plus the floor, whatever the per-request maximum. The 20% ratio is the default of Finagle's `RetryBudget` and of Envoy's retry budget; the floor lets a quiet instance retry at all; the cap of 10 keeps a long healthy period from banking a burst for the start of an outage. Credits come from requests, not from fallback attempts, so falling back never funds more retries. The budget is withdrawn before the wait, so an exhausted budget never costs a wait; the breaker is asked after the wait (decision 6), and a withdrawal the breaker refuses or the client cancels is refunded. A budget in Redis was rejected: the bound per instance times the instance count is the fleet bound, and the hot path gains no store call.

6. **Every attempt goes through the breaker.** A retry calls `Allow` after its wait and gets its own ticket: holding a ticket through the wait would occupy a half-open probe slot and could belong to a generation that ended meanwhile. An open breaker, or a half-open one with every probe slot taken, stops retries on that backend and the walker moves to the next one. Each failed attempt records its own failure. A failing request therefore feeds the breaker up to `1 + max` failures, so the breaker opens after fewer requests than without retries.

7. **Streams retry only while opening.** The walker retries a failure that `StreamComplete` returns before a stream exists. Once a stream is returned, nothing is retried, as before.

8. **Idempotency key from the request ID.** Every upstream attempt carries `Idempotency-Key: pusaq-<request id>`. The request ID is generated by the gateway (128 random bits), never taken from the client, so keys do not collide across tenants and no client can choose another request's key. The key is the same on the retries and the fallbacks of one request; a fallback on the same provider account deduplicates as intended, other providers ignore it. A client-sent `Idempotency-Key` is neither forwarded nor folded in: a client that reuses its key for different bodies would make an upstream that honors keys return one prompt's completion for another, the cross-request leak constitution rule 2 guards the cache against; the gateway admits, charges and accounts each client request on its own, so deduplicating client retries upstream would make provider billing and gateway accounting disagree; and the header is not part of the accepted OpenAI subset (rule 7). The key is sent whether or not retries are on: mocks ignore it and no published number depends on it. `ChatRequest.IdempotencyKey` carries it with `json:"-"`, so the body is unchanged.

9. **Tokens are charged once.** The token charge stays before the chain and the reconciliation stays one finalizer reading the answering attempt's usage; a retry neither charges nor reconciles (constitution rule 4). What a failed attempt consumed upstream is unknown to the gateway, as it already is for a failed fallback attempt.

10. **Retries visible apart from fallbacks.** Cost events carry `retries` on every event, like `attempts`; `attempts − retries` is the number of backends attempted. `retry_denied` names `budget` and `deadline` when a retryable failure was not retried for that reason; reaching the configured maximum is expected and an open breaker is visible in `breaker_state`, so neither is listed. A retried failure logs `backend failed, retrying` with its wait; `backend failed, trying next` and `backend breaker open, skipping` keep their meaning, so the event report reads fallbacks as before.

11. **Off by default.** `PUSAQ_RETRY_MAX` defaults to 0. The breaker-detection recipe predicts detection at the 11th failing request and reads `attempts == 2` as a request that paid the primary once before falling back; with retries on, each failing request pays up to `1 + max` attempts, so the breaker opens after about 11 / (1 + max) requests and `attempts == 2` stops meaning one fallback. The regional-failover recipe takes its gateway environment from the runtime module and reports attempt counts. Off by default, both reproduce their published numbers; turning retries on is an explicit choice with its own measurement.

## Risks / Trade-offs

- Retries add latency before fallback: at most the window plus the attempts started inside it.
- An upstream that ignores the idempotency key may bill a failed attempt that generated tokens; the cost event prices the answering attempt only, so that spend is not visible in it.
- With retries on, a short burst of transient failures fills the breaker's minimum samples with fewer requests and can open it sooner.
- The budget is per instance: n instances allow n times the reserve and the floor.
- The budget is a float: a ratio such as 0.2 is not exact in binary, and a million credits of 0.2 sum to 200000 within 4·10⁻⁶.
- The event report counts a backend's failure once per request (`backend failed, trying next`); retried failures appear in `retries`, not in its per-backend failure counts.
