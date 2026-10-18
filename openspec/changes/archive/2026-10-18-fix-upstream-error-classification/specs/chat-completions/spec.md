# chat-completions

## MODIFIED Requirements

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
