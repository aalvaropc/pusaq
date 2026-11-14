## ADDED Requirements

### Requirement: Same-backend retries are bounded per request

When retries are enabled, an attempt that fails before any response byte reaches the client with a retryable failure SHALL be retried on the same backend before the request falls back. Retryable failures are a transport failure before the upstream answered (connection refused or reset, DNS or TLS failure, a timeout before the response headers) and the statuses 408, 429, 500, 502, 503 and 504. A provider's rejection (400, 413, 422), any other upstream failure, and a client that left SHALL never be retried. A request SHALL make at most the configured number of retries in total across its chain. Before the n-th retry of a backend the gateway SHALL wait a delay drawn uniformly from zero to min(2 s, 200 ms · 2^(n−1)), or the upstream's `Retry-After` when it gives a positive one. A retry SHALL start only if its wait ends before the request's retry deadline — the configured window after the request's first upstream attempt, or the request context's deadline when earlier; a wait that would end after the deadline SHALL send the request to its next backend without waiting. Every retry SHALL ask the backend's breaker for its own admission after the wait and SHALL feed its own outcome; a breaker that refuses the admission SHALL stop retries on that backend. The token charge SHALL be made once per request and reconciled once, whatever the number of retries. Retries SHALL be off by default; the maximum, the budget ratio and the window SHALL come from configuration, be validated at startup, and be stated in the startup log.

#### Scenario: Retryable failure retried on the same backend

- **WHEN** retries are enabled and the primary fails once with a transport failure before answering, or with 408, 429, 500, 502, 503 or 504, and then answers
- **THEN** the response comes from the primary after one retry and the fallback backend is not called

#### Scenario: Rejections and other failures are not retried

- **WHEN** retries are enabled and the primary answers 400, 413 or 422, or answers 401, 404, or 200 without a completion
- **THEN** the primary is attempted once; a rejection ends the request as `invalid_request` and any other failure goes to the fallback backend

#### Scenario: Per-request maximum

- **WHEN** retries are enabled with a maximum of 2 and the primary keeps failing with 500
- **THEN** the primary is attempted three times and the request then goes to the fallback backend

#### Scenario: Retry-After inside the window

- **WHEN** the primary answers 429 with `Retry-After: 1` and the retry window has more than one second left
- **THEN** the gateway waits one second and retries the primary

#### Scenario: Retry-After beyond the window

- **WHEN** the primary answers 503 with a `Retry-After` longer than what remains of the retry window
- **THEN** the gateway does not wait, sends the request to the fallback backend, and the cost event carries `retry_denied` `deadline`

#### Scenario: Open breaker stops retries

- **WHEN** a retry's wait ends with the backend's breaker open
- **THEN** no further attempt reaches that backend and the request goes to the next backend

#### Scenario: Client leaves during the wait

- **WHEN** the client disconnects while a retry waits
- **THEN** no further attempt is made, no breaker records an outcome for it, and the cost event records `client_canceled`

#### Scenario: Retries charge tokens once

- **WHEN** a request with token limiting is retried and then served
- **THEN** its tokens were charged once at admission and reconciled once against the answering attempt's usage

#### Scenario: Retries off by default

- **WHEN** no retry setting is configured
- **THEN** each backend of the chain is attempted at most once per request

#### Scenario: Retry configuration is validated

- **WHEN** the retry maximum is outside 0–5, the budget ratio is not a finite number in (0, 1], or the window is not a positive duration of at most 10 minutes
- **THEN** the gateway fails to start with a configuration error

### Requirement: An instance retry budget bounds retry load

Each gateway instance SHALL keep one retry budget shared by all its requests: a token bucket that each request's first upstream attempt credits with the configured ratio, that refills at one retry per second, that holds at most ten retries, and from which every retry takes one. A retry SHALL be made only when the budget holds one; a retry the budget refuses SHALL send the request to its next backend. A retry taken from the budget and then not made — its backend's breaker refused it or the client left — SHALL be returned. Over any interval of length T in which N requests reached an upstream, an instance SHALL make at most 10 + T · 1/s + ratio · N retries.

#### Scenario: A failing upstream cannot multiply load

- **WHEN** every attempt fails, retries allow two per request, the budget ratio is 0.2, and 1000 requests arrive with no time passing
- **THEN** the instance makes at most 210 retries, against 2000 without the budget

#### Scenario: Budget refusal is visible

- **WHEN** a retryable failure finds the budget empty
- **THEN** the request goes to its next backend without waiting and its cost event carries `retry_denied` `budget`

#### Scenario: Floor refills an idle budget

- **WHEN** the budget is empty and one second passes
- **THEN** one retry is available

## MODIFIED Requirements

### Requirement: Fallback to equivalent backends

When a request's backend fails (upstream error) after the retries its failure allows, or its breaker is open, the gateway SHALL try the route's configured fallback backends in order, entering each at most once, preserving the model contract. If every backend fails or is open, the gateway SHALL respond with the normalized `upstream_error`. Each attempt's outcome, retries included, SHALL feed that backend's breaker: an upstream error as a failure, any answer the backend produced as a success. A provider's rejection of the request (400, 413, 422) SHALL end the chain as the client's error. When the client's request ends, the gateway SHALL stop the chain, record no outcome for the interrupted attempt, and release its half-open probe slot.

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

#### Scenario: Retries come before fallback

- **WHEN** retries are enabled and the primary keeps failing with 503 while the fallback is healthy
- **THEN** the primary's breaker records one failure per attempt and the fallback serves the request after the primary's retries

### Requirement: Streams fall back only before the first byte

For streaming requests, retries and fallback SHALL apply only to failures that occur before the response stream starts. Once a stream has begun, a mid-stream failure SHALL end the stream with its usual accounting; the gateway SHALL NOT silently restart a stream on the same or another backend. A started stream's outcome SHALL feed its backend's breaker when the stream ends: an upstream failure or a missing `[DONE]` as a failure, a completed stream as a success, and a client disconnect as no outcome.

#### Scenario: Pre-stream failure fails over

- **WHEN** a streaming request's primary backend refuses before any chunk is delivered
- **THEN** the stream is served by the fallback backend

#### Scenario: Pre-stream failure is retried

- **WHEN** retries are enabled and a streaming request's primary answers 503 before the stream opens and then opens it
- **THEN** the stream is served by the primary after one retry

#### Scenario: Mid-stream failure does not restart

- **WHEN** a stream fails after chunks were delivered
- **THEN** the stream ends with the upstream-error accounting and no second backend is tried

#### Scenario: Mid-stream failure is not retried

- **WHEN** retries are enabled and a stream fails after it started
- **THEN** the stream ends with the upstream-error accounting and no second attempt reaches any backend

#### Scenario: Mid-stream failures count against the backend

- **WHEN** streams from one backend keep failing after their first bytes
- **THEN** that backend's breaker records the failures and opens like it would for failures before the first byte
