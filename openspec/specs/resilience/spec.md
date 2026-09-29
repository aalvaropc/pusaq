# resilience Specification

## Purpose
How the gateway survives upstream failure: a circuit breaker per backend converts a dead provider from inherited downtime into a fast local decision, and fallback keeps equivalent traffic flowing through the remaining backends.

## Requirements

### Requirement: Circuit breaker per upstream backend

The gateway SHALL track each upstream backend's recent outcomes and open that backend's breaker when the failure rate over a sliding window crosses a configured threshold with at least a minimum number of samples. While open, requests SHALL fail fast for that backend without attempting the call. After a cooldown, the breaker SHALL allow a bounded number of probe requests (half-open): a probe success closes the breaker, a probe failure reopens it. The window size, minimum samples, failure threshold, cooldown, and probe count SHALL come from configuration, be validated at startup, and be stated in the startup log. Every state transition SHALL be observable as a log record naming the backend, the previous state, and the new state. Detection time (failure start to breaker open) and recovery time (failure end to breaker closed) SHALL be measurable against a provider that fails at a known instant, by one reproducible command whose results are published with their protocol.

#### Scenario: Breaker opens on sustained failures

- **WHEN** a backend's failure rate over the window crosses the threshold with the minimum sample count met
- **THEN** subsequent requests to that backend are not attempted while the breaker is open

#### Scenario: Insufficient samples never open the breaker

- **WHEN** a backend has fewer results than the minimum sample count, even if all are failures
- **THEN** its breaker stays closed

#### Scenario: Half-open probes are bounded and decisive

- **WHEN** the cooldown elapses and requests arrive
- **THEN** at most the configured number of probes reach the backend; a probe success closes the breaker and a probe failure reopens it

#### Scenario: State transitions are observable

- **WHEN** a breaker changes state
- **THEN** the gateway emits one record with the backend name, the state it left, and the state it entered

#### Scenario: Breaker configuration is validated

- **WHEN** the window is not positive, the minimum samples exceed the window, the threshold is outside (0, 1), the cooldown is not positive, or the probe count is not positive
- **THEN** the gateway fails to start with a configuration error

#### Scenario: Detection and recovery measured against a failure window

- **WHEN** the primary backend starts failing at a known instant under steady traffic with a healthy fallback
- **THEN** no client request fails, the breaker's open and closed instants are recorded, and detection and recovery times are computed from those records against the failure window

### Requirement: Fallback to equivalent backends

When a request's backend fails (upstream error) or its breaker is open, the gateway SHALL try the route's configured fallback backends in order, at most once each, preserving the model contract. If every backend fails or is open, the gateway SHALL respond with the normalized `upstream_error`. Failures and successes of each attempt SHALL feed that backend's breaker.

#### Scenario: Failover serves the request

- **WHEN** the primary backend fails and a fallback backend is healthy
- **THEN** the response comes from the fallback backend and the request succeeds

#### Scenario: Open primary is skipped without an attempt

- **WHEN** the primary's breaker is open
- **THEN** the request goes directly to the next backend, paying no timeout against the dead one

#### Scenario: Everything down is a normalized error

- **WHEN** all backends in the chain fail or are open
- **THEN** the response is 502 `upstream_error` and one cost event records the failed attempts

### Requirement: Streams fall back only before the first byte

For streaming requests, fallback SHALL apply only to failures that occur before the response stream starts. Once a stream has begun, a mid-stream failure SHALL end the stream with its usual accounting; the gateway SHALL NOT silently restart a stream on another backend.

#### Scenario: Pre-stream failure fails over

- **WHEN** a streaming request's primary backend refuses before any chunk is delivered
- **THEN** the stream is served by the fallback backend

#### Scenario: Mid-stream failure does not restart

- **WHEN** a stream fails after chunks were delivered
- **THEN** the stream ends with the upstream-error accounting and no second backend is tried
