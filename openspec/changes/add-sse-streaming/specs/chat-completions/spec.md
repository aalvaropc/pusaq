# chat-completions

## ADDED Requirements

### Requirement: Streaming completions over SSE

When a chat-completions request sets `"stream": true`, the gateway SHALL respond with Server-Sent Events (`Content-Type: text/event-stream`): OpenAI-shaped `chat.completion.chunk` events passed through from the provider as they arrive, terminated by a `data: [DONE]` event. Streamed responses SHALL carry the same `X-Pusaq-Model` and request-ID headers as buffered ones. Chunks SHALL be forwarded without waiting for the stream to complete.

#### Scenario: Streamed round-trip

- **WHEN** a client requests a completion with `"stream": true` for an allowed model
- **THEN** the response is 200 with `Content-Type: text/event-stream`, delivers one or more `chat.completion.chunk` events followed by `data: [DONE]`, and carries the served-model and request-ID headers

#### Scenario: Pre-stream failures are normal errors

- **WHEN** a streaming request fails validation, names an unknown model, or the provider refuses the stream before any chunk is delivered
- **THEN** the gateway responds with the normalized JSON error shape and status, not an SSE response
