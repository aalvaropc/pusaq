# rate-limiting Specification

## Purpose
Per-tenant distributed request limits. The limit must hold exactly when any number of gateway replicas share it, deny with a contract clients can act on, and degrade toward availability — not outage — when its backing store fails.

## Requirements

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

### Requirement: Token-based limits with reservation and reconciliation

With rate limiting enabled, the gateway SHALL additionally limit each tenant by LLM tokens per minute. After the request's model is resolved — and only then — the gateway SHALL atomically charge the tenant's token bucket with the request's input-token estimate plus an output reservation (the request's `max_tokens` when present, a configured default otherwise). When the request's actual usage becomes known, the gateway SHALL atomically reconcile the difference between charged and actual back into the bucket. Reconciliation SHALL happen exactly once per charged request — on success, on error, and at the end of a streamed response. When actual usage never becomes known, the full charge SHALL stand.

#### Scenario: Token budget exhausted

- **WHEN** a tenant's token bucket cannot cover a request's input estimate plus output reservation
- **THEN** the request is rejected with 429 `rate_limited`, a positive `retry_after_ms`, and no provider is called

#### Scenario: Unused reservation is returned

- **WHEN** a completion reserves more output tokens than the provider actually generated
- **THEN** the difference is returned to the tenant's bucket after completion, and available tokens never exceed the bucket capacity

#### Scenario: Streamed responses reconcile at stream end

- **WHEN** a streamed completion delivers its usage in the stream
- **THEN** reconciliation runs when the stream ends, using that usage

#### Scenario: Unknown usage keeps the charge

- **WHEN** a request's actual usage never becomes known
- **THEN** the original charge stands and nothing is refunded

### Requirement: Request limits and token limits are independent dimensions

RPM and TPM SHALL be separate buckets per tenant: exhausting one dimension SHALL NOT consume or block the other beyond rejecting the requests it governs.

#### Scenario: Token rejection does not consume request quota beyond the attempt

- **WHEN** a tenant is rejected by the token limit
- **THEN** subsequent small requests within both limits are admitted
