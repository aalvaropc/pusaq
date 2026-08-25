# rate-limiting

## Purpose

Per-tenant distributed request limits. The limit must hold exactly when any number of gateway replicas share it, deny with a contract clients can act on, and degrade toward availability — not outage — when its backing store fails.

## ADDED Requirements

### Requirement: Per-tenant token bucket limits requests

With rate limiting enabled, the gateway SHALL limit each tenant's requests with a token bucket of configured capacity (burst) and sustained refill rate, keyed by tenant, before any provider work happens. A new tenant's bucket SHALL start full, so its configured burst is immediately usable. Buckets idle beyond a bounded time SHALL be reclaimed and start full again.

#### Scenario: Burst allowed, then limited

- **WHEN** a tenant with bucket capacity B sends B requests at once and then one more
- **THEN** the first B are admitted and the next is rejected

#### Scenario: Refill restores admission

- **WHEN** a limited tenant waits long enough for the refill rate to accrue a token
- **THEN** its next request is admitted, and available tokens never exceed the bucket capacity

#### Scenario: Tenants are isolated

- **WHEN** one tenant exhausts its bucket
- **THEN** other tenants' requests are unaffected

### Requirement: The limit is exact across replicas

The admission decision SHALL be atomic in the shared store: under concurrent requests from any number of gateway replicas, the number admitted within a window SHALL NOT exceed the bucket capacity plus accrued refill — never a multiple of it.

#### Scenario: Concurrent replicas cannot overrun

- **WHEN** many concurrent requests race through multiple gateway instances sharing one store, with no refill accruing
- **THEN** exactly the bucket capacity is admitted and everything beyond is rejected

### Requirement: Denials carry an actionable contract

A rate-limited request SHALL be rejected with the normalized error code `rate_limited` and HTTP 429, a `retry_after_ms` value computed from the bucket state in the error body, a `Retry-After` header, and a cost event with the error outcome.

#### Scenario: 429 with retry guidance

- **WHEN** a request is rejected by the limiter
- **THEN** the response is 429 with code `rate_limited`, a positive `retry_after_ms`, a `Retry-After` header, and one cost event records the rejection

### Requirement: Store failure degrades open

If the limiter's backing store is unreachable or errors, the gateway SHALL admit the request (fail-open) and log the degradation — temporarily inexact limits are preferred over a total outage caused by the limiter itself. Rate limiting disabled by configuration SHALL be announced with a startup warning, never silent.

#### Scenario: Redis outage does not reject traffic

- **WHEN** the backing store is down and a request arrives
- **THEN** the request is admitted and the degradation is logged

#### Scenario: Disabled limiting is explicit

- **WHEN** the gateway starts without a configured store address
- **THEN** it serves without rate limiting and logs a warning at startup
