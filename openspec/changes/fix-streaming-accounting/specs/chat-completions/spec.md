# chat-completions

## MODIFIED Requirements

### Requirement: Streaming completions over SSE

When a chat-completions request sets `"stream": true`, the gateway SHALL respond with Server-Sent Events (`Content-Type: text/event-stream`): OpenAI-shaped `chat.completion.chunk` events passed through from the provider as they arrive, terminated by a `data: [DONE]` event. The provider request SHALL ask for usage (`stream_options.include_usage`). Streamed responses SHALL carry the same `X-Pusaq-Model` and request-ID headers as buffered ones. Chunks SHALL be forwarded without waiting for the stream to complete. A stream completes only when the provider sends `[DONE]`; when the provider fails mid-stream or ends without `[DONE]`, the gateway SHALL append one SSE event carrying the normalized error shape with code `upstream_error` and end the stream.

#### Scenario: Streamed round-trip

- **WHEN** a client requests a completion with `"stream": true` for an allowed model
- **THEN** the response is 200 with `Content-Type: text/event-stream`, delivers one or more `chat.completion.chunk` events followed by `data: [DONE]`, and carries the served-model and request-ID headers

#### Scenario: Pre-stream failures are normal errors

- **WHEN** a streaming request fails validation, names an unknown model, or the provider refuses the stream before any chunk is delivered
- **THEN** the gateway responds with the normalized JSON error shape and status, not an SSE response

#### Scenario: Provider asked for usage

- **WHEN** the gateway opens a streamed provider call
- **THEN** the provider request carries `stream_options` with `include_usage` set, and buffered calls carry no `stream_options`

#### Scenario: Mid-stream failure is reported in-band

- **WHEN** the provider fails after chunks were delivered, or its stream ends without `[DONE]`
- **THEN** the client receives an SSE event whose data is `{"error":{"code":"upstream_error",…}}`, no `[DONE]`, and the cost event records `upstream_error`
