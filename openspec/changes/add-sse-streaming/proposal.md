# Proposal: add-sse-streaming

## Why

Chat completions exist but only buffered; real LLM clients stream. `stream: true` with SSE passthrough completes the V1 API subset the design doc commits to — and forces the one architectural question streaming raises: how the "exactly one cost event per request" invariant survives a response that outlives the pipeline call.

## What Changes

- `POST /v1/chat/completions` accepts `"stream": true` and responds with Server-Sent Events: OpenAI-shaped `chat.completion.chunk` events terminated by `data: [DONE]`, passed through from the provider chunk by chunk.
- Provider interface gains a streaming method; the HTTP adapter uses a header-bounded (not whole-exchange-bounded) client for streams so long generations are not killed by the buffered timeout.
- The accounting invariant extends to streams: the cost event is emitted exactly once **when the stream ends** (fully delivered, upstream failure, or client disconnect), with token usage parsed from the stream's final usage chunk.
- The mock provider learns to stream: canned content split into chunks, a final usage chunk, then `[DONE]`.
- New normalized code `stream_interrupted` for streams the client abandoned before completion (accounting outcome only — nothing is written to a gone client).

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `chat-completions`: adds the streaming requirement (SSE shape, passthrough, termination).
- `request-pipeline`: the exactly-one-cost-event requirement now defines emission timing for streamed responses.

## Impact

- Modified: `internal/core` (streaming response support, `stream_interrupted` code), `internal/provider` (streaming method + stream client), `internal/gateway` (accounting finalization at stream end, SSE passthrough in the HTTP adapter, usage-scanning stream wrapper), `internal/mockprovider` (chunked SSE mode).
- No new external Go dependencies.
