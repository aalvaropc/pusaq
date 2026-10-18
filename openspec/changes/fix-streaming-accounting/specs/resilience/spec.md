# resilience

## MODIFIED Requirements

### Requirement: Streams fall back only before the first byte

For streaming requests, fallback SHALL apply only to failures that occur before the response stream starts. Once a stream has begun, a mid-stream failure SHALL end the stream with its usual accounting; the gateway SHALL NOT silently restart a stream on another backend. A started stream's outcome SHALL feed its backend's breaker when the stream ends: an upstream failure or a missing `[DONE]` as a failure, a completed stream as a success, and a client disconnect as no outcome.

#### Scenario: Pre-stream failure fails over

- **WHEN** a streaming request's primary backend refuses before any chunk is delivered
- **THEN** the stream is served by the fallback backend

#### Scenario: Mid-stream failure does not restart

- **WHEN** a stream fails after chunks were delivered
- **THEN** the stream ends with the upstream-error accounting and no second backend is tried

#### Scenario: Mid-stream failures count against the backend

- **WHEN** streams from one backend keep failing after their first bytes
- **THEN** that backend's breaker records the failures and opens like it would for failures before the first byte
