# chat-completions

## MODIFIED Requirements

### Requirement: Upstream failures are normalized and bounded

Provider errors SHALL map to the normalized error code `upstream_error` with HTTP 502, without leaking the provider's raw response to the client. Provider calls SHALL be bounded by a configurable timeout; a timeout is an upstream error. For streamed calls the timeout SHALL bound connecting, the TLS handshake, the response headers, and every silence between chunks.

#### Scenario: Provider 5xx becomes 502

- **WHEN** the provider responds with a server error
- **THEN** the gateway responds 502 with code `upstream_error`, the cost event records the error outcome, and the provider's raw body is not forwarded

#### Scenario: Provider timeout becomes 502

- **WHEN** the provider does not respond within the configured upstream timeout
- **THEN** the gateway responds 502 with code `upstream_error`

#### Scenario: Stalled stream ends as upstream error

- **WHEN** a streamed provider response sends its first bytes and then stays silent longer than the configured upstream timeout
- **THEN** the stream ends and the cost event records the error outcome with code `upstream_error`
