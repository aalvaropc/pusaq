# request-pipeline

## Purpose

The composable stage pipeline every gateway request traverses. It defines how cross-cutting concerns (identification, accounting, and — in other changes — limiting, policy, caching, routing) wrap the terminal handler, and it guarantees that every request is accounted for exactly once, no matter how it ends.

## ADDED Requirements

### Requirement: Requests traverse a composable stage pipeline

The gateway SHALL process every API request through an ordered chain of stages wrapping a terminal handler. Stage order SHALL be fixed by composition, and each stage SHALL be able to short-circuit (respond without invoking the rest of the chain) or pass control onward.

#### Scenario: Stages execute in composition order

- **WHEN** a request enters a pipeline composed of stages S1, S2 and terminal handler H
- **THEN** S1 observes the request before S2, S2 before H, and the response flows back in reverse order

#### Scenario: A stage short-circuits

- **WHEN** a stage produces a response or error without calling the next stage
- **THEN** downstream stages and the terminal handler are not invoked, and the pipeline still completes normally (including accounting)

### Requirement: Every request is assigned a request ID

The pipeline SHALL assign each request a unique request ID before any other processing, and the ID SHALL be included in the response headers and in the request's cost event.

#### Scenario: Request ID present on response and cost event

- **WHEN** any request is processed by the pipeline
- **THEN** the response carries the request ID in a header, and the emitted cost event contains the same ID

### Requirement: Exactly one cost event per request, regardless of outcome

The pipeline SHALL emit exactly one cost event for every request it admits — on success, on handler error, and on panic. A panic inside the pipeline SHALL be recovered and converted into a normalized internal error response; it SHALL NOT crash the process and SHALL NOT suppress the cost event.

#### Scenario: Cost event on success

- **WHEN** a request completes successfully
- **THEN** exactly one cost event is emitted, recording at least request ID, outcome, and total latency

#### Scenario: Cost event on error

- **WHEN** the terminal handler returns an error
- **THEN** the client receives a normalized error response and exactly one cost event is emitted with the error outcome

#### Scenario: Cost event on panic

- **WHEN** any stage or the terminal handler panics
- **THEN** the process survives, the client receives a normalized internal error, and exactly one cost event is emitted with the panic outcome

### Requirement: Errors are rendered in one normalized shape

All error responses produced by the gateway SHALL share a single JSON error shape containing a stable machine-readable code and a human-readable message, with an appropriate HTTP status.

#### Scenario: Handler error is normalized

- **WHEN** any pipeline stage or handler fails
- **THEN** the response body is the normalized error JSON and the HTTP status matches the error's category
