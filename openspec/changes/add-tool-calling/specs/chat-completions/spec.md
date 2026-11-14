## MODIFIED Requirements

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

## ADDED Requirements

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
