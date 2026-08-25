# request-pipeline

## MODIFIED Requirements

### Requirement: Exactly one cost event per request, regardless of outcome

The pipeline SHALL emit exactly one cost event for every request it admits — on success, on handler error, and on panic. A panic inside the pipeline SHALL be recovered and converted into a normalized internal error response; it SHALL NOT crash the process and SHALL NOT suppress the cost event. For responses delivered as a stream, the cost event SHALL be emitted exactly once when the stream ends — whether it completed, failed upstream, or was abandoned by the client — and SHALL include usage reported within the stream when present.

#### Scenario: Cost event on success

- **WHEN** a request completes successfully
- **THEN** exactly one cost event is emitted, recording at least request ID, outcome, and total latency

#### Scenario: Cost event on error

- **WHEN** the terminal handler returns an error
- **THEN** the client receives a normalized error response and exactly one cost event is emitted with the error outcome

#### Scenario: Cost event on panic

- **WHEN** any stage or the terminal handler panics
- **THEN** the process survives, the client receives a normalized internal error, and exactly one cost event is emitted with the panic outcome

#### Scenario: Cost event at stream end

- **WHEN** a streamed response is fully delivered
- **THEN** exactly one cost event is emitted at stream end, with latency covering the full stream and usage parsed from the stream

#### Scenario: Cost event on abandoned stream

- **WHEN** the client disconnects before a streamed response completes
- **THEN** exactly one cost event is emitted with an error outcome identifying the interrupted stream
