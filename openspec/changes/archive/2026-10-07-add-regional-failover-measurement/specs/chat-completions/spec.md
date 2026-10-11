# chat-completions

## ADDED Requirements

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
