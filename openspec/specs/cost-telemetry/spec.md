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

Every cost event SHALL include at least: request ID, timestamp, operation, outcome (success, error code, or panic), and total latency. The schema SHALL be extensible so later changes can add tenant, model, token, and price fields without breaking consumers.

#### Scenario: Minimum fields present

- **WHEN** any cost event is emitted
- **THEN** it contains request ID, timestamp, operation, outcome, and latency

### Requirement: Emitter shuts down cleanly

On gateway shutdown, the emitter SHALL stop accepting new events and flush already-buffered events to the sink before the process exits.

#### Scenario: Buffered events survive shutdown

- **WHEN** the gateway begins graceful shutdown while events sit in the buffer
- **THEN** those events are delivered to the sink before the process exits
