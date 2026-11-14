# chat-completions Specification

## Purpose
The OpenAI-compatible chat-completions subset — the contract that lets an application switch to Pusaq by changing only its base URL. Covers request validation, model-alias resolution, provider proxying, served-model reporting, and upstream error mapping. Covers buffered and streamed (SSE) responses.

## Requirements

### Requirement: OpenAI-compatible chat completions endpoint

The gateway SHALL expose `POST /v1/chat/completions` accepting the OpenAI request subset (`model`, `messages`, optionally `max_tokens`, `temperature`, `stream`, `metadata`, `tools`, `tool_choice`, `parallel_tool_calls`) and returning the OpenAI response subset (`id`, `object`, `created`, `model`, `system_fingerprint` when the provider reports one, `choices` with `message` — its `tool_calls` included — and `finish_reason`, `usage` with token counts). `created` SHALL be the provider's Unix timestamp when it reports one and the gateway's Unix time when the response is built otherwise. `temperature` SHALL be forwarded to the provider exactly as sent, including an explicit 0, and omitted when the client omitted it. `max_tokens`, when present, SHALL be a positive integer. Fields that would change the response contract and that the gateway does not implement SHALL be rejected as `invalid_request` naming the field: at the top level `functions`, `function_call`, `response_format`, `n` other than 1, `logprobs`, `top_logprobs`, `audio`, `modalities` other than `["text"]`, `prediction`, `web_search_options`, `max_completion_tokens`; in messages `function_call`, `audio`, and content given as an array of parts. One of these fields set to `null` or `false` counts as absent; other fields SHALL be ignored. Requests SHALL traverse the request pipeline like any other operation.

#### Scenario: Completion round-trip

- **WHEN** a client POSTs a valid chat-completions request with an allowed model
- **THEN** the response is 200 with an OpenAI-shaped body including choices and usage, and one cost event is emitted for the request

#### Scenario: Creation time and fingerprint

- **WHEN** the provider's completion carries `created` and `system_fingerprint`
- **THEN** the buffered response carries both values unchanged; when the provider sends neither, `created` is the gateway's current Unix time and `system_fingerprint` is absent

#### Scenario: Malformed request rejected

- **WHEN** the body is not valid JSON, `messages` is empty, `model` is missing, or `max_tokens` is not a positive integer
- **THEN** the gateway responds 400 with the normalized error shape and code `invalid_request`, without calling any provider

#### Scenario: Unsupported field rejected

- **WHEN** a request carries `functions`, `function_call`, `response_format`, a message with `function_call`, or content given as an array of parts
- **THEN** the gateway responds 400 `invalid_request` with a message and a `param` naming the field, without calling any provider

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

Every chat-completions response — success or error after model resolution — SHALL carry the `X-Pusaq-Model` header with the route's physical model: the upstream model that configuration maps the resolved alias to, which every backend in the fallback chain is called with. The header SHALL NOT be taken from the provider's answer. The `model` field of a buffered response body SHALL be the model name the provider reported, and streamed chunks SHALL keep the provider's own `model` values; either can differ from the header, for example a dated version behind an upstream alias.

#### Scenario: Header on success

- **WHEN** a completion succeeds
- **THEN** the response includes `X-Pusaq-Model` with the route's physical model

#### Scenario: Body reports the provider's model

- **WHEN** the provider answers a buffered completion reporting a model name other than the route's physical model
- **THEN** `X-Pusaq-Model` carries the route's physical model and the body's `model` carries the name the provider reported

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

When a chat-completions request sets `"stream": true`, the gateway SHALL respond with Server-Sent Events (`Content-Type: text/event-stream`): OpenAI-shaped `chat.completion.chunk` events forwarded byte-for-byte after each complete event is validated, terminated by a `data: [DONE]` event. The provider request SHALL ask for usage (`stream_options.include_usage`). Streamed responses SHALL carry the same `X-Pusaq-Model` and request-ID headers as buffered ones. Chunks SHALL be forwarded without waiting for the stream to complete. A stream completes only when the provider sends `[DONE]`; when the provider fails mid-stream or ends without `[DONE]`, the gateway SHALL append one SSE event carrying the normalized error shape with code `upstream_error` and end the stream.

#### Scenario: Streamed round-trip

- **WHEN** a client requests a completion with `"stream": true` for an allowed model
- **THEN** the response is 200 with `Content-Type: text/event-stream`, delivers one or more `chat.completion.chunk` events followed by `data: [DONE]`, and carries the served-model and request-ID headers

#### Scenario: Pre-stream failures are normal errors

- **WHEN** a streaming request fails validation, names an unknown model, or the provider rejects the request before a successful streaming HTTP response
- **THEN** the gateway responds with the normalized JSON error shape and status, not an SSE response

#### Scenario: Provider asked for usage

- **WHEN** the gateway opens a streamed provider call
- **THEN** the provider request carries `stream_options` with `include_usage` set, and buffered calls carry no `stream_options`

#### Scenario: Mid-stream failure is reported in-band

- **WHEN** the provider fails after chunks were delivered, or its stream ends without `[DONE]`
- **THEN** the client receives an SSE event whose data is `{"error":{"code":"upstream_error",…}}`, no `[DONE]`, and the cost event records `upstream_error`

#### Scenario: Provider error payload is withheld

- **WHEN** an event contains a non-null top-level error or declares event type error
- **THEN** none of that event's bytes are forwarded, one generic upstream_error event terminates the stream, the breaker records failure, no fallback is attempted after streaming starts, and no sample enters the shadow cache

#### Scenario: Event memory is bounded

- **WHEN** a response streams at least 64 MiB of content in bounded events
- **THEN** retained scanner memory does not grow with total content and the fingerprint matches the buffered content hash

#### Scenario: Oversized event is withheld

- **WHEN** an event exceeds 1 MiB including its framing
- **THEN** its bytes are withheld and the stream ends with one generic error event

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

When the replica is labeled with a region, every response — success, error, readiness — SHALL carry the `X-Pusaq-Region` header with that label. A request MAY declare `metadata.run_id`, a string of at most 64 characters, read before admission so that every cost event of the request — including a rejection by the rate limiter — carries it; any other value SHALL be rejected as `invalid_request` before admission. An authenticated request that reaches a replica inside an injected not-ready window SHALL be refused with 503 `not_ready` and a `Retry-After`.

#### Scenario: Region header on every response

- **WHEN** a labeled replica answers a completion, an error, or a readiness probe
- **THEN** the response carries `X-Pusaq-Region` with the label

#### Scenario: Run id validated

- **WHEN** a request declares a non-string or oversized `metadata.run_id`
- **THEN** the response is 400 `invalid_request` with param `metadata.run_id` before admission, and its one cost event carries no run id

#### Scenario: Run id on an early rejection

- **WHEN** a request declaring `metadata.run_id` is rejected by the rate limiter
- **THEN** its cost event carries the run id

#### Scenario: Refused while not ready

- **WHEN** an authenticated request arrives during an injected not-ready window
- **THEN** the response is 503 `not_ready` with a `Retry-After` header, the region header and a request ID, and no provider is called

### Requirement: Upstream URLs are versioned API base URLs

Each upstream URL SHALL be the base of an OpenAI-compatible API with its version segment included, without a trailing slash. Chat completions, buffered and streamed, SHALL be sent to the base URL followed by `/chat/completions`. A trailing slash in configuration SHALL be ignored.

#### Scenario: Versioned base

- **WHEN** an upstream is configured as `http://host:9090/v1`
- **THEN** its chat calls go to `http://host:9090/v1/chat/completions`

#### Scenario: Provider base with its own version path

- **WHEN** an upstream is configured as `https://host/v1beta/openai/`
- **THEN** its buffered and streamed chat calls go to `https://host/v1beta/openai/chat/completions`

### Requirement: Outbound provider endpoints restrict credential transport

Configured provider base URLs SHALL be absolute HTTP or HTTPS URLs without user information, query strings, fragments, or invalid ports. Endpoints carrying credentials SHALL use HTTPS, except explicit development mode with localhost or a literal loopback IP. Keyless HTTP mocks SHALL remain supported. Provider clients SHALL reject redirects without forwarding any body or credentials to the redirect destination. Configuration errors SHALL not echo endpoint URLs or credentials.

#### Scenario: Redirect does not resend private data

- **WHEN** a buffered or streaming provider returns a 307 or 308 redirect
- **THEN** the destination receives zero requests and the original attempt becomes a normalized upstream error

#### Scenario: Invalid or plaintext keyed endpoint

- **WHEN** an endpoint violates URL restrictions or uses credentials over HTTP outside the explicit loopback development exception
- **THEN** startup fails without echoing its URL or key

#### Scenario: Compatible versioned endpoint

- **WHEN** a valid HTTPS base URL contains its API version path
- **THEN** requests preserve the configured path and carry credentials only to that endpoint

### Requirement: Upstream attempts carry an idempotency key

Every upstream attempt of a completion, buffered or streamed, SHALL carry an `Idempotency-Key` header of the form `pusaq-<request id>`, built from the request ID the gateway generated and returned in `X-Pusaq-Request-Id`: the same on every retry and every fallback attempt of one request, and different for every request. The key SHALL NOT appear in the upstream request body. A client-sent `Idempotency-Key` SHALL neither be forwarded nor folded into the key, so no client can choose the key another request or tenant reaches the upstream with. The gateway SHALL NOT claim that an upstream deduplicates attempts: an upstream that ignores the header treats each attempt as a new request.

#### Scenario: One key across retries and fallback

- **WHEN** a request is retried on the primary and then served by the fallback backend
- **THEN** every attempt carries the same `Idempotency-Key`, `pusaq-` followed by the request ID the response reports

#### Scenario: Distinct requests carry distinct keys

- **WHEN** a client sends the same completion twice
- **THEN** the upstream receives two different keys

#### Scenario: A client key is not forwarded

- **WHEN** a client sends `Idempotency-Key: k1` with a completion
- **THEN** the upstream receives the gateway's key and never `k1`

#### Scenario: Streamed attempts carry the key

- **WHEN** a streamed completion is opened upstream
- **THEN** the upstream request carries the request's key in its headers and not in its body

### Requirement: Basic tool calling

The gateway SHALL accept basic function calling. `tools` SHALL be an array of 1 to 128 tools whose compact JSON is at most 256 KiB; each tool SHALL have type `function`, a `function.name` matching `^[a-zA-Z0-9_-]{1,64}$` and unique among the tools, an optional string `function.description`, and an optional `function.parameters` that is a JSON object; `function.strict` other than `null` or `false` SHALL be rejected. `tool_choice` SHALL be `none`, `auto`, `required`, or `{"type":"function","function":{"name":…}}` naming a declared tool. `parallel_tool_calls` SHALL be a boolean. `tool_choice` and `parallel_tool_calls` SHALL require `tools`. An assistant message MAY carry `tool_calls`, a non-empty array whose entries have an `id` unique within the request, type `function`, a `function.name` matching the same pattern, and a string `function.arguments`; its `content` MAY then be null or absent. A `tool` message SHALL carry string `content` and a `tool_call_id` naming a tool call of an earlier assistant message that no earlier tool message answered. `tool_calls` on another role, `tool_call_id` on another role, and the legacy `function` role SHALL be rejected. Every violation SHALL be rejected as `invalid_request` with a `param` naming the field, before any charge or provider call. The provider request of every attempt, retries and fallbacks included, SHALL carry the tools with their parameters as the client sent them, `tool_choice` reduced to its string or to the function object with its name, `parallel_tool_calls`, and the messages' tool calls and tool-call IDs. Buffered responses SHALL carry each choice's `tool_calls` (`id`, `type`, `function.name`, `function.arguments`) and the provider's `finish_reason`, with `content` null when the message has tool calls and no text; streamed tool-call deltas SHALL be forwarded unchanged. The input-token estimate SHALL count tool definitions and tool-call names and arguments next to message content; provider-reported usage SHALL still reconcile the charge and price the request.

#### Scenario: Tool definitions forwarded

- **WHEN** a request declares tools, a `tool_choice` naming one of them and `parallel_tool_calls: false`
- **THEN** the provider request carries the tools with their parameters in the client's key order, `tool_choice` `{"type":"function","function":{"name":…}}` and `parallel_tool_calls: false`, for buffered and streamed completions alike, and a retried attempt sends the same body

#### Scenario: Malformed tool definitions rejected

- **WHEN** a tool has a type other than `function`, a name outside the pattern or repeated, `parameters` that are not an object, or `strict: true`, or the request declares no tool, more than 128 tools or more than 256 KiB of them
- **THEN** the gateway responds 400 `invalid_request` with `param` `tools[].type`, `tools[].function.name`, `tools[].function.parameters`, `tools[].function.strict` or `tools`, and calls no provider

#### Scenario: Tool choice validated

- **WHEN** `tool_choice` is a string other than `none`, `auto` or `required`, names a function the request does not declare, or `tool_choice` or `parallel_tool_calls` is set without `tools`
- **THEN** the gateway responds 400 `invalid_request` with `param` `tool_choice`, `tool_choice.function.name` or `parallel_tool_calls`, and calls no provider

#### Scenario: Malformed tool calls rejected

- **WHEN** an assistant tool call repeats an ID, has a type other than `function`, an invalid name or non-string arguments, `tool_calls` is empty or appears on a user message, or a message uses the `function` role
- **THEN** the gateway responds 400 `invalid_request` with `param` naming the field, such as `messages[].tool_calls[].id` or `messages[].role`, and calls no provider

#### Scenario: Tool result answers an earlier call

- **WHEN** a `tool` message names an ID no earlier assistant message made, an ID an earlier tool message answered, carries no `tool_call_id`, or has null content
- **THEN** the gateway responds 400 `invalid_request` with `param` `messages[].tool_call_id` or `messages[].content`, and calls no provider

#### Scenario: Tool call returned buffered

- **WHEN** the provider answers a buffered completion with a tool call and `finish_reason` `tool_calls`
- **THEN** the response's message carries the tool call's ID, type, name and arguments, `content` null, `finish_reason` `tool_calls`, and the provider's usage

#### Scenario: Tool-call deltas streamed unchanged

- **WHEN** the provider streams a tool call as a first delta with its ID and name followed by argument fragments
- **THEN** the client receives every event byte for byte, then `data: [DONE]`, and the charge reconciles against the stream's usage

#### Scenario: Tool round trip

- **WHEN** a client sends tools, receives a tool call, and sends the conversation back with the assistant's tool call and a `tool` message answering it
- **THEN** the provider receives the assistant message with its tool call and the tool message with its `tool_call_id`, and the gateway returns the provider's text answer

#### Scenario: Estimate counts tools

- **WHEN** two requests differ only in their tool definitions
- **THEN** the token charge of each counts the compact JSON of its definitions, and the cost event carries the provider's usage and the cost priced from it

### Requirement: The mock provider calls tools deterministically

The mock provider SHALL answer a request whose `tools` are present, whose `tool_choice` is not `none` and whose last message is not a tool result with exactly one tool call: to the function `tool_choice` names, otherwise the first tool; with arguments built from the top-level properties of its parameters; with an ID unique within the conversation; with `content` null and `finish_reason` `tool_calls`. Streamed, the first delta SHALL carry the ID and name and the arguments SHALL follow in fragments of at most 8 bytes that never split a character. Every other request SHALL get a text answer. Every answer SHALL report usage.

#### Scenario: Forced tool called

- **WHEN** a request declares two tools and `tool_choice` names the second
- **THEN** the mock answers one call to the second tool with deterministic arguments and `finish_reason` `tool_calls`

#### Scenario: Streamed arguments arrive in fragments

- **WHEN** a streamed request declares a tool whose parameters have properties
- **THEN** the mock's stream carries the tool call's ID and name in one delta and its arguments across several deltas, whose concatenation equals the buffered arguments, followed by `finish_reason` `tool_calls`, usage and `[DONE]`

#### Scenario: Text after a tool result

- **WHEN** the last message of a request with tools is a tool result, or `tool_choice` is `none`
- **THEN** the mock answers text with `finish_reason` `stop`
