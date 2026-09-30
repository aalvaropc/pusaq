# chat-completions

## MODIFIED Requirements

### Requirement: OpenAI-compatible chat completions endpoint

The gateway SHALL expose `POST /v1/chat/completions` accepting the OpenAI request subset (`model`, `messages`, optionally `max_tokens`, `temperature`, `stream`, `metadata`) and returning the OpenAI response subset (`id`, `object`, `model`, `choices` with `message` and `finish_reason`, `usage` with token counts). `temperature` SHALL be forwarded to the provider exactly as sent, including an explicit 0, and omitted when the client omitted it. Requests SHALL traverse the request pipeline like any other operation.

#### Scenario: Completion round-trip

- **WHEN** a client POSTs a valid chat-completions request with an allowed model
- **THEN** the response is 200 with an OpenAI-shaped body including choices and usage, and one cost event is emitted for the request

#### Scenario: Malformed request rejected

- **WHEN** the body is not valid JSON, `messages` is empty, or `model` is missing
- **THEN** the gateway responds 400 with the normalized error shape and code `invalid_request`, without calling any provider

#### Scenario: Temperature forwarded as sent

- **WHEN** a client sets `temperature` to 0, to 1.2, or omits it
- **THEN** the provider request carries 0, 1.2, or no temperature respectively, for buffered and streamed completions alike
