# Design: fix-streaming-accounting

## Context

`provider.HTTP.StreamComplete` marshals `ChatRequest` with `stream: true` only. `priceOnFinalize` returns early when usage is zero, and the spend finalizer adds only positive costs. `usageScanningStream` (`internal/gateway/completions.go`) splits on `\n`, parses each `data:` line alone, and passes read errors through; `finalizingStream` maps EOF to success. The chain walker records a breaker success when a stream opens. See proposal.md.

## Goals / Non-Goals

**Goals:**

- Every stream that a provider answered ends with a cost and a spend, measured or estimated, and says which.
- A client always learns that a stream failed.
- A stream completes only with `[DONE]`.

**Non-Goals:**

- Rewriting provider chunks: bytes pass through untouched; the error event is appended, never substituted.
- A total stream-size limit: only a single event is bounded.

## Decisions

1. **Usage request in the adapter.** The streaming call marshals an unexported wrapper that embeds `ChatRequest` and adds `stream` and `stream_options`; `ChatRequest.Stream` goes away.

2. **Estimate from the reservation.** The reservation is the same upper bound the token limiter charges. The estimate goes into the cost, never into the usage, so reconciliation and reported usage stay provider-reported; `core.Cost.Estimated` marks it. Requests no provider answered still carry no cost.

3. **Event scanner with a terminal error.** Bytes are split into lines on any SSE terminator (a `\r` at a chunk boundary waits for the next byte), `data:` values join with `\n`, a blank line dispatches. A read error other than EOF or `context.Canceled`, an EOF without `[DONE]`, or an event over 1 MiB makes the scanner emit `\n\ndata: {"error":{"code":"upstream_error",…}}\n\n` and then return the error; the leading blank lines close any partial event. The stream ends as `upstream_error` without changes to the stage that emits the event.

4. **Breaker outcome at stream end.** The chain walker returns the served backend's breaker instead of recording the opening; the buffered path records success at once, the streaming path registers a finalizer that records a failure, a success, or `Release` for a client disconnect.

## Risks / Trade-offs

- A client that did not ask for usage now receives the final usage chunk with empty `choices`, as OpenAI sends it; the docs already promised that chunk.
- The estimate charges the whole reservation for a stream aborted after a few tokens. Accepted: it is the bound the token limit already uses, and the event marks it.
