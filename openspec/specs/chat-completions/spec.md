# chat-completions Specification

## Purpose
The OpenAI-compatible chat-completions subset — the contract that lets an application switch to Pusaq by changing only its base URL. Covers request validation, model-alias resolution, provider proxying, served-model reporting, and upstream error mapping. Non-streaming only; streaming is a separate capability.

## Requirements

### Requirement: OpenAI-compatible chat completions endpoint

The gateway SHALL expose `POST /v1/chat/completions` accepting the OpenAI request subset (`model`, `messages`, optionally `max_tokens`) and returning the OpenAI response subset (`id`, `object`, `model`, `choices` with `message` and `finish_reason`, `usage` with token counts). Requests SHALL traverse the request pipeline like any other operation.

#### Scenario: Completion round-trip

- **WHEN** a client POSTs a valid chat-completions request with an allowed model
- **THEN** the response is 200 with an OpenAI-shaped body including choices and usage, and one cost event is emitted for the request

#### Scenario: Malformed request rejected

- **WHEN** the body is not valid JSON, `messages` is empty, or `model` is missing
- **THEN** the gateway responds 400 with the normalized error shape and code `invalid_request`, without calling any provider

### Requirement: Models are addressed by alias

The gateway SHALL accept only configured model aliases (initially `pusaq/fast`, `pusaq/best`, `pusaq/auto`) in the `model` field and SHALL resolve each alias to a provider and upstream model from configuration. The physical model each alias resolves to SHALL be configurable per alias; configuration naming an unknown alias SHALL be rejected at startup. Physical upstream model names SHALL live in configuration only.

#### Scenario: Alias resolved from configuration

- **WHEN** a request names a configured alias
- **THEN** the request is forwarded to the provider and upstream model that configuration maps that alias to

#### Scenario: Per-alias model override

- **WHEN** configuration maps an alias to a specific physical model
- **THEN** requests for that alias are served by that model and report it in `X-Pusaq-Model`

#### Scenario: Unknown alias in configuration rejected at startup

- **WHEN** the route configuration names an alias the gateway does not define
- **THEN** the gateway fails to start with a configuration error

#### Scenario: Unknown model rejected

- **WHEN** a request names a model that is not a configured alias
- **THEN** the gateway responds 400 with code `invalid_request` naming the rejected model, without calling any provider

### Requirement: Served model is reported

Every chat-completions response — success or error after model resolution — SHALL carry the `X-Pusaq-Model` header with the physical model the gateway served (or attempted to serve).

#### Scenario: Header on success

- **WHEN** a completion succeeds
- **THEN** the response includes `X-Pusaq-Model` with the upstream model that produced it

### Requirement: Upstream failures are normalized and bounded

Provider errors SHALL map to the normalized error code `upstream_error` with HTTP 502, without leaking the provider's raw response to the client. Provider calls SHALL be bounded by a configurable timeout; a timeout is an upstream error.

#### Scenario: Provider 5xx becomes 502

- **WHEN** the provider responds with a server error
- **THEN** the gateway responds 502 with code `upstream_error`, the cost event records the error outcome, and the provider's raw body is not forwarded

#### Scenario: Provider timeout becomes 502

- **WHEN** the provider does not respond within the configured upstream timeout
- **THEN** the gateway responds 502 with code `upstream_error`

### Requirement: Streaming completions over SSE

When a chat-completions request sets `"stream": true`, the gateway SHALL respond with Server-Sent Events (`Content-Type: text/event-stream`): OpenAI-shaped `chat.completion.chunk` events passed through from the provider as they arrive, terminated by a `data: [DONE]` event. Streamed responses SHALL carry the same `X-Pusaq-Model` and request-ID headers as buffered ones. Chunks SHALL be forwarded without waiting for the stream to complete.

#### Scenario: Streamed round-trip

- **WHEN** a client requests a completion with `"stream": true` for an allowed model
- **THEN** the response is 200 with `Content-Type: text/event-stream`, delivers one or more `chat.completion.chunk` events followed by `data: [DONE]`, and carries the served-model and request-ID headers

#### Scenario: Pre-stream failures are normal errors

- **WHEN** a streaming request fails validation, names an unknown model, or the provider refuses the stream before any chunk is delivered
- **THEN** the gateway responds with the normalized JSON error shape and status, not an SSE response

### Requirement: Upstream calls are authenticated when configured

An upstream backend MAY carry an API key in configuration. When present, the gateway SHALL authenticate every call to that backend with it; when absent, no credentials SHALL be sent. Upstream keys SHALL never appear in logs or cost events.

#### Scenario: Keyed upstream receives credentials

- **WHEN** an upstream is configured with an API key and a request is routed to it
- **THEN** the upstream call carries that key as a bearer credential

#### Scenario: Keyless upstream receives none

- **WHEN** an upstream has no configured key
- **THEN** its calls carry no Authorization credential
