# cost-telemetry Specification

## Purpose
Asynchronous emission of per-request cost events — the raw material of Pusaq's cost accounting. Telemetry is an observer of the hot path, never a participant: losing an event is acceptable and counted; delaying a user response is not.

## Requirements

### Requirement: Cost events are emitted asynchronously

The gateway SHALL emit cost events without blocking the request/response path. Event delivery SHALL happen outside the request lifecycle; a slow or unavailable sink SHALL NOT add latency to any request.

#### Scenario: Slow sink does not delay responses

- **WHEN** the telemetry sink is slow or stalled
- **THEN** request latency is unaffected and responses continue to be served

### Requirement: Overflow drops events and counts the drops

When the telemetry buffer is full, the gateway SHALL drop new cost events rather than block, and SHALL count every dropped event in an observable drop metric.

#### Scenario: Full buffer drops instead of blocking

- **WHEN** cost events are produced faster than the sink drains them and the buffer is full
- **THEN** the producing request completes without waiting and the drop counter increases by the number of dropped events

### Requirement: Cost events carry the accounting fields

Every cost event SHALL include at least: request ID, timestamp, operation, tenant, outcome (success, error code, or panic), and total latency. For requests that resolve a model, the event SHALL additionally include the requested model (alias) and the served model, and — when the provider reports usage — prompt and completion token counts. The schema SHALL remain extensible so price fields can be added without breaking consumers.

#### Scenario: Minimum fields present

- **WHEN** any cost event is emitted
- **THEN** it contains request ID, timestamp, operation, tenant, outcome, and latency

#### Scenario: Completion events carry model and usage

- **WHEN** a chat-completions request succeeds
- **THEN** its cost event contains the requested alias, the served model, and the provider-reported prompt and completion token counts

### Requirement: Emitter shuts down cleanly

On gateway shutdown, the emitter SHALL stop accepting new events and flush already-buffered events to the sink before the process exits.

#### Scenario: Buffered events survive shutdown

- **WHEN** the gateway begins graceful shutdown while events sit in the buffer
- **THEN** those events are delivered to the sink before the process exits

### Requirement: Completion events record tokens charged

For requests charged against a token limit, the cost event SHALL record the tokens charged (input estimate plus output reservation) alongside the actual usage, so reservation error is measurable from the event stream alone.

#### Scenario: Charged and actual both present

- **WHEN** a completion charged against the token bucket finishes with known usage
- **THEN** its cost event contains both the charged tokens and the actual prompt/completion counts

### Requirement: Events record the serving backend and attempts

For requests that reach the routing flow, the cost event SHALL record which upstream backend actually served the request and how many backend attempts were made, so failover is observable from the event stream alone.

#### Scenario: Failover visible in the event

- **WHEN** a request is served by a fallback backend after the primary failed
- **THEN** its cost event names the fallback backend and an attempt count greater than one

### Requirement: Completion events carry cost against a versioned price table

When a price table is configured, the cost event of every completion whose provider-reported usage becomes known SHALL carry the request's cost in US dollars — prompt tokens at the served model's input rate plus completion tokens at its output rate — together with the version label of the table that priced it. Prices SHALL be keyed by physical model and SHALL come from configuration only. With a table configured, every physical model a route resolves to SHALL be priced, and the gateway SHALL refuse to start otherwise. A table SHALL require a version label. Without a table, events SHALL carry no cost fields and the gateway SHALL announce that once at startup.

#### Scenario: Priced completion carries cost and table version

- **WHEN** a buffered completion succeeds with provider-reported usage and its served model is priced
- **THEN** its cost event carries the cost computed from that usage and the price table version

#### Scenario: Streamed completion is priced at stream end

- **WHEN** a streamed completion delivers its usage in the stream
- **THEN** the cost event emitted at stream end carries the cost computed from that usage

#### Scenario: Unknown usage means no cost fields

- **WHEN** a request ends without provider-reported usage, such as an upstream error before any answer
- **THEN** its cost event carries no cost fields

#### Scenario: No price table means no cost fields and one startup notice

- **WHEN** the gateway starts without a price table
- **THEN** it logs one notice that cost events carry no cost fields, and no cost event carries them

#### Scenario: Route model without a price is a startup error

- **WHEN** a price table is configured and a route resolves to a physical model the table does not price
- **THEN** the gateway fails to start with a configuration error naming the model

#### Scenario: Version required with a table

- **WHEN** a price table is configured without a version label, or a version label without a table
- **THEN** the gateway fails to start with a configuration error

### Requirement: Events record the feature tag and routing reason

For requests that resolve a model, the cost event SHALL record how the requested alias was resolved (`route_reason`, using the same vocabulary as the response header) and, when the request declared one, its feature tag — for any alias, not only `pusaq/auto` — so cost can be attributed per feature and per routing decision from the event stream alone.

#### Scenario: Tagged auto request

- **WHEN** a request for `pusaq/auto` with a feature tag is served
- **THEN** its cost event carries the tag and the reason `rule:<tag>` or a default reason

#### Scenario: Direct alias carries the tag and reason direct

- **WHEN** a request for a concrete alias declares a feature tag
- **THEN** its cost event carries that tag and the reason `direct`
