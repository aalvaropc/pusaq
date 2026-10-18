# chat-completions

## MODIFIED Requirements

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
