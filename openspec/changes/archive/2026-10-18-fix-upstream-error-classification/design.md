# Design: fix-upstream-error-classification

## Context

`internal/provider/http.go` maps any non-200 status and any `client.Do` error to `CodeUpstream`. Both fallback loops in `internal/gateway/completions.go` record `Record(err == nil || code != upstream)` and continue on upstream errors, without checking whether the client's context is still alive. See proposal.md.

## Goals / Non-Goals

**Goals:**

- A client's malformed request never trips a breaker or reaches a second backend.
- A canceled request stops the chain and leaves every breaker as it was.
- A 200 that carries no completion is an upstream error.

**Non-Goals:**

- Retrying 429 with backoff, a retry budget, or idempotency keys; one attempt per backend stays the rule.

## Decisions

1. **Status classification in the adapter.** One function maps a non-200 status to an error: 400, 413 and 422 become `invalid_request` with the status in the message and no body; everything else stays `upstream_error`. 401, 403 and 404 stay upstream errors because they describe that backend's key or model configuration, which another backend may not share.

2. **One chain walker for both paths.** The buffered and streaming loops become one generic function that takes the attempt as a closure. It checks the request context before each attempt and after a failed one; when the context has ended it releases the breaker slot and returns `client_canceled`. Cancellation is detected from the gateway's own context, not from the returned error: an upstream `Client.Timeout` also matches `context.DeadlineExceeded`.

3. **`Breaker.Release`.** An allowed attempt that produced no outcome gives back its half-open probe slot without moving the breaker; in the closed state it is a no-op.

4. **`client_canceled` (499).** A new stable code for requests whose client left before an answer existed; nothing is written to a gone client, so the status is for logs only. Streams keep `stream_interrupted` once they have started.

5. **Completion checks on 200.** The body is read up to the cap plus one byte; going over the cap is an upstream error. A body with an `error` object or without choices is an upstream error.

## Risks / Trade-offs

- A provider that answers 400 to every request because of a gateway-side bug no longer trips its breaker; the 400s reach clients instead. Accepted: the alternative lets any tenant open every breaker.
- `Record(true)` on a provider rejection adds a success sample and closes a half-open breaker; the backend did answer, so that is its health.
