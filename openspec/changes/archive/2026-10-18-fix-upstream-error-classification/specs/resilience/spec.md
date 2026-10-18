# resilience

## MODIFIED Requirements

### Requirement: Fallback to equivalent backends

When a request's backend fails (upstream error) or its breaker is open, the gateway SHALL try the route's configured fallback backends in order, at most once each, preserving the model contract. If every backend fails or is open, the gateway SHALL respond with the normalized `upstream_error`. Each attempt's outcome SHALL feed that backend's breaker: an upstream error as a failure, any answer the backend produced as a success. A provider's rejection of the request (400, 413, 422) SHALL end the chain as the client's error. When the client's request ends, the gateway SHALL stop the chain, record no outcome for the interrupted attempt, and release its half-open probe slot.

#### Scenario: Failover serves the request

- **WHEN** the primary backend fails and a fallback backend is healthy
- **THEN** the response comes from the fallback backend and the request succeeds

#### Scenario: Open primary is skipped without an attempt

- **WHEN** the primary's breaker is open
- **THEN** the request goes directly to the next backend, paying no timeout against the dead one

#### Scenario: Everything down is a normalized error

- **WHEN** all backends in the chain fail or are open
- **THEN** the response is 502 `upstream_error` and one cost event records the failed attempts

#### Scenario: A rejected request trips no breaker

- **WHEN** a client repeats a request the primary rejects with 400
- **THEN** every response is 400 `invalid_request`, no fallback backend is called, and every breaker stays closed

#### Scenario: A canceled request feeds no breaker

- **WHEN** the client disconnects during an attempt
- **THEN** no further backend is attempted and no breaker records a failure
