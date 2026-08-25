# resilience

## Purpose

How the gateway survives upstream failure: a circuit breaker per backend converts a dead provider from inherited downtime into a fast local decision, and fallback keeps equivalent traffic flowing through the remaining backends.

## ADDED Requirements

### Requirement: Circuit breaker per upstream backend

The gateway SHALL track each upstream backend's recent outcomes and open that backend's breaker when the failure rate over a sliding window crosses a configured threshold with at least a minimum number of samples. While open, requests SHALL fail fast for that backend without attempting the call. After a cooldown, the breaker SHALL allow a bounded number of probe requests (half-open): a probe success closes the breaker, a probe failure reopens it.

#### Scenario: Breaker opens on sustained failures

- **WHEN** a backend's failure rate over the window crosses the threshold with the minimum sample count met
- **THEN** subsequent requests to that backend are not attempted while the breaker is open

#### Scenario: Insufficient samples never open the breaker

- **WHEN** a backend has fewer results than the minimum sample count, even if all are failures
- **THEN** its breaker stays closed

#### Scenario: Half-open probes are bounded and decisive

- **WHEN** the cooldown elapses and requests arrive
- **THEN** at most the configured number of probes reach the backend; a probe success closes the breaker and a probe failure reopens it

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
