# chat-completions Specification

## Purpose
The OpenAI-compatible chat-completions subset — the contract that lets an application switch to Pusaq by changing only its base URL. Covers request validation, model-alias resolution, provider proxying, served-model reporting, and upstream error mapping. Covers buffered and streamed (SSE) responses.

## Requirements

### Requirement: OpenAI-compatible chat completions endpoint

The gateway SHALL expose `POST /v1/chat/completions` accepting the OpenAI request subset (`model`, `messages`, optionally `max_tokens`, `temperature`, `stream`, `metadata`) and returning the OpenAI response subset (`id`, `object`, `model`, `choices` with `message` and `finish_reason`, `usage` with token counts). `temperature` SHALL be forwarded to the provider exactly as sent, including an explicit 0, and omitted when the client omitted it. `max_tokens`, when present, SHALL be a positive integer. Fields that would change the response contract and that the gateway does not implement SHALL be rejected as `invalid_request` naming the field: at the top level `tools`, `tool_choice`, `functions`, `function_call`, `parallel_tool_calls`, `response_format`, `n` other than 1, `logprobs`, `top_logprobs`, `audio`, `modalities` other than `["text"]`, `prediction`, `web_search_options`, `max_completion_tokens`; in messages `tool_calls`, `tool_call_id`, `function_call`, `audio`, and content given as an array of parts. A field set to `null` or `false` counts as absent; other fields SHALL be ignored. Requests SHALL traverse the request pipeline like any other operation.

#### Scenario: Completion round-trip

- **WHEN** a client POSTs a valid chat-completions request with an allowed model
- **THEN** the response is 200 with an OpenAI-shaped body including choices and usage, and one cost event is emitted for the request

#### Scenario: Malformed request rejected

- **WHEN** the body is not valid JSON, `messages` is empty, `model` is missing, or `max_tokens` is not a positive integer
- **THEN** the gateway responds 400 with the normalized error shape and code `invalid_request`, without calling any provider

#### Scenario: Unsupported field rejected

- **WHEN** a request carries `tools`, `response_format`, a message with `tool_calls`, or content given as an array of parts
- **THEN** the gateway responds 400 `invalid_request` with a message naming the field, without calling any provider

#### Scenario: Harmless fields are accepted

- **WHEN** a request carries `user`, `seed`, `n: 1`, or `logprobs: false`
- **THEN** the request is served as if those fields were absent

#### Scenario: Temperature forwarded as sent

- **WHEN** a client sets `temperature` to 0, to 1.2, or omits it
- **THEN** the provider request carries 0, 1.2, or no temperature respectively, for buffered and streamed completions alike

### Requirement: Models are addressed by alias

The gateway SHALL accept only configured model aliases (`pusaq/fast`, `pusaq/best`, `pusaq/auto`) in the `model` field. `pusaq/fast` and `pusaq/best` SHALL each resolve to a provider and upstream model from configuration; the physical model each resolves to SHALL be configurable per alias. `pusaq/auto` SHALL have no physical model of its own and SHALL resolve to one of the concrete aliases through the routing rules. Configuration naming an unknown alias, or naming `pusaq/auto` as a route, SHALL be rejected at startup. Physical upstream model names SHALL live in configuration only.

#### Scenario: Alias resolved from configuration

- **WHEN** a request names a concrete alias
- **THEN** the request is forwarded to the provider and upstream model that configuration maps that alias to

#### Scenario: Per-alias model override

- **WHEN** configuration maps a concrete alias to a specific physical model
- **THEN** requests for that alias are served by that model and report it in `X-Pusaq-Model`

#### Scenario: Unknown alias in configuration rejected at startup

- **WHEN** the route configuration names an alias the gateway does not define
- **THEN** the gateway fails to start with a configuration error

#### Scenario: Auto alias in route configuration rejected at startup

- **WHEN** the route configuration assigns a physical model to `pusaq/auto`
- **THEN** the gateway fails to start with a configuration error explaining that the alias resolves through routing rules

#### Scenario: Unknown model rejected

- **WHEN** a request names a model that is not a configured alias
- **THEN** the gateway responds 400 with code `invalid_request` naming the rejected model, without calling any provider

### Requirement: pusaq/auto resolves through feature-tag routing rules

The gateway SHALL resolve `pusaq/auto` to a concrete alias using an explicit table from configuration keyed by the request's `metadata.feature_tag`: the rule for the tag when one exists, the default alias otherwise. The table SHALL have a default entry, every tag SHALL use letters, digits, `.`, `_` and `-`, and every target SHALL be a concrete configured alias; violations SHALL be rejected at startup. Every completion response — success, streamed, or error after resolution — SHALL carry `X-Pusaq-Route-Reason` with one of `direct` (a concrete alias was requested), `rule:<tag>`, `default:untagged`, or `default:unknown_tag`. A `metadata.feature_tag` that is not a string, is longer than 64 characters, or uses other characters SHALL be rejected as `invalid_request` without calling any provider.

#### Scenario: Tagged request follows its rule

- **WHEN** a request for `pusaq/auto` carries a feature tag that a rule names
- **THEN** it is served by that rule's alias and the response reports `rule:<tag>`

#### Scenario: Untagged request uses the default

- **WHEN** a request for `pusaq/auto` carries no feature tag
- **THEN** it is served by the default alias and the response reports `default:untagged`

#### Scenario: Unknown tag uses the default

- **WHEN** a request for `pusaq/auto` carries a feature tag no rule names
- **THEN** it is served by the default alias and the response reports `default:unknown_tag`

#### Scenario: Route reason reported on every response

- **WHEN** a completion is served, streamed, or fails upstream after its alias was resolved
- **THEN** the response carries `X-Pusaq-Route-Reason`, and a concrete alias reports `direct`

#### Scenario: Routing rules are validated at startup

- **WHEN** the routing rules lack a default entry, target `pusaq/auto` or an unknown alias, repeat a tag, or use a tag with other characters
- **THEN** the gateway fails to start with a configuration error

#### Scenario: Non-string feature tag rejected

- **WHEN** a request's `metadata.feature_tag` is not a string
- **THEN** the gateway responds 400 with code `invalid_request` and calls no provider

#### Scenario: Oversized or malformed feature tag rejected

- **WHEN** a request's `metadata.feature_tag` is longer than 64 characters or contains characters outside letters, digits, `.`, `_` and `-`
- **THEN** the gateway responds 400 with code `invalid_request` and calls no provider

### Requirement: Served model is reported

Every chat-completions response — success or error after model resolution — SHALL carry the `X-Pusaq-Model` header with the physical model the gateway served (or attempted to serve).

#### Scenario: Header on success

- **WHEN** a completion succeeds
- **THEN** the response includes `X-Pusaq-Model` with the upstream model that produced it

### Requirement: Upstream failures are normalized and bounded

Provider errors SHALL map to the normalized error code `upstream_error` with HTTP 502, without leaking the provider's raw response to the client. A provider answer of 400, 413 or 422 SHALL instead map to `invalid_request` with HTTP 400, still without the provider's body. A 200 answer whose body carries an `error` object, carries no choices, or exceeds the read cap SHALL be an upstream error. Provider calls SHALL be bounded by a configurable timeout; a timeout is an upstream error. For streamed calls the timeout SHALL bound connecting, the TLS handshake, the response headers, and every silence between chunks. When the client's request ends before any answer exists, the cost event SHALL record the code `client_canceled`.

#### Scenario: Provider 5xx becomes 502

- **WHEN** the provider responds with a server error
- **THEN** the gateway responds 502 with code `upstream_error`, the cost event records the error outcome, and the provider's raw body is not forwarded

#### Scenario: Provider rejects the request

- **WHEN** the provider responds 400, 413 or 422
- **THEN** the gateway responds 400 with code `invalid_request`, without the provider's body and without trying another backend

#### Scenario: A 200 without a completion is an upstream error

- **WHEN** the provider responds 200 with an `error` object, without choices, or with a body over the read cap
- **THEN** the gateway treats the attempt as an upstream error

#### Scenario: Provider timeout becomes 502

- **WHEN** the provider does not respond within the configured upstream timeout
- **THEN** the gateway responds 502 with code `upstream_error`

#### Scenario: Stalled stream ends as upstream error

- **WHEN** a streamed provider response sends its first bytes and then stays silent longer than the configured upstream timeout
- **THEN** the stream ends and the cost event records the error outcome with code `upstream_error`

#### Scenario: Canceled request

- **WHEN** the client disconnects while the provider call is in flight
- **THEN** no other backend is called and the cost event records the error outcome with code `client_canceled`

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

### Requirement: Upstream calls are authenticated when configured

An upstream backend MAY carry an API key in configuration. When present, the gateway SHALL authenticate every call to that backend with it; when absent, no credentials SHALL be sent. Upstream keys SHALL never appear in logs or cost events, and configuration errors SHALL never echo an upstream URL. Two upstreams whose names map to the same key variable SHALL fail startup.

#### Scenario: Keyed upstream receives credentials

- **WHEN** an upstream is configured with an API key and a request is routed to it
- **THEN** the upstream call carries that key as a bearer credential

#### Scenario: Keyless upstream receives none

- **WHEN** an upstream has no configured key
- **THEN** its calls carry no Authorization credential

#### Scenario: Colliding key variables fail startup

- **WHEN** two upstreams are named `my-api` and `my_api`
- **THEN** the gateway fails to start, naming both upstreams and the shared key variable

### Requirement: Responses identify the serving region and requests may declare a run

When the replica is labeled with a region, every response — success, error, readiness — SHALL carry the `X-Pusaq-Region` header with that label. A request MAY declare `metadata.run_id`, a string of at most 64 characters, read before admission so that every cost event of the request — including a rejection by the rate limiter — carries it; any other value SHALL be rejected as `invalid_request` before admission. A request that reaches a replica inside an injected not-ready window SHALL be refused with 503 `not_ready` and a `Retry-After`.

#### Scenario: Region header on every response

- **WHEN** a labeled replica answers a completion, an error, or a readiness probe
- **THEN** the response carries `X-Pusaq-Region` with the label

#### Scenario: Run id validated

- **WHEN** a request declares a non-string or oversized `metadata.run_id`
- **THEN** the response is 400 `invalid_request` before admission, with no cost event

#### Scenario: Run id on an early rejection

- **WHEN** a request declaring `metadata.run_id` is rejected by the rate limiter
- **THEN** its cost event carries the run id

#### Scenario: Refused while not ready

- **WHEN** a request arrives during an injected not-ready window
- **THEN** the response is 503 `not_ready` with a `Retry-After` header and the region header, and no provider is called

### Requirement: Upstream URLs are versioned API base URLs

Each upstream URL SHALL be the base of an OpenAI-compatible API with its version segment included, without a trailing slash. Chat completions, buffered and streamed, SHALL be sent to the base URL followed by `/chat/completions`. A trailing slash in configuration SHALL be ignored.

#### Scenario: Versioned base

- **WHEN** an upstream is configured as `http://host:9090/v1`
- **THEN** its chat calls go to `http://host:9090/v1/chat/completions`

#### Scenario: Provider base with its own version path

- **WHEN** an upstream is configured as `https://host/v1beta/openai/`
- **THEN** its buffered and streamed chat calls go to `https://host/v1beta/openai/chat/completions`
