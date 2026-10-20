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

In strict mode (the default), the admission decision SHALL be atomic in the shared store: under concurrent requests from any number of gateway replicas, the number admitted within a window SHALL NOT exceed the bucket capacity plus accrued refill — never a multiple of it. This property SHALL be verifiable against real gateway processes by one reproducible command whose results are published together with the protocol that produced them.

#### Scenario: Concurrent replicas cannot overrun

- **WHEN** many concurrent requests race through multiple gateway instances sharing one store, with no refill accruing
- **THEN** exactly the bucket capacity is admitted and everything beyond is rejected

#### Scenario: Exactness holds across process replicas under sustained overload

- **WHEN** two gateway processes behind a load balancer receive requests from one tenant at a multiple of its sustained rate for a duration T, starting from a full bucket of capacity B with refill rate r
- **THEN** the admitted count lies within `[B + r·(T − ε), B + r·(T + ε)]` for a small declared tolerance ε, every other request is rejected with 429 `rate_limited`, the upstream's served count equals the admitted count, and no request was admitted by fail-open

#### Scenario: A co-tenant is unaffected during another tenant's overload

- **WHEN** a second tenant sends requests well under its own limit while the first tenant is overloading its bucket
- **THEN** the second tenant receives no rejections

### Requirement: Denials carry an actionable contract

A rate-limited request SHALL be rejected with the normalized error code `rate_limited` and HTTP 429, a `retry_after_ms` value computed from the bucket state in the error body, a `Retry-After` header, and a cost event with the error outcome.

#### Scenario: 429 with retry guidance

- **WHEN** a request is rejected by the limiter
- **THEN** the response is 429 with code `rate_limited`, a positive `retry_after_ms`, a `Retry-After` header, and one cost event records the rejection

### Requirement: Store failure degrades open

If the limiter's backing store is unreachable or errors, the gateway SHALL admit the request (fail-open) and log the degradation — temporarily inexact limits are preferred over a total outage caused by the limiter itself — unless the tenant's policy sets `fail_closed`, in which case the request SHALL be rejected with 503 `store_unavailable` and no provider called. Rate limiting disabled by configuration SHALL be announced with a startup warning, never silent.

#### Scenario: Redis outage does not reject traffic

- **WHEN** the backing store is down and a request arrives
- **THEN** the request is admitted and the degradation is logged

#### Scenario: Fail-closed tenant is rejected during an outage

- **WHEN** the backing store is down and a request arrives from a tenant whose policy sets `fail_closed`
- **THEN** the response is 503 `store_unavailable`, no provider is called, and the degradation is logged

#### Scenario: Disabled limiting is explicit

- **WHEN** the gateway starts without a configured store address
- **THEN** it serves without rate limiting and logs a warning at startup

### Requirement: Token-based limits with reservation and reconciliation

With rate limiting enabled, the gateway SHALL additionally limit each tenant by LLM tokens per minute unless token limiting is explicitly disabled by configuration. After the request's model is resolved — and only then — the gateway SHALL atomically charge the tenant's token bucket with the request's input-token estimate plus an output reservation (the request's `max_tokens` when present, a configured default otherwise). A request whose input estimate plus reservation exceeds the bucket's capacity SHALL be rejected as `invalid_request` without any charge. The configured default reservation SHALL fit the bucket's capacity; a configuration where it does not SHALL be rejected at startup. When the request's actual usage becomes known, the gateway SHALL atomically reconcile the difference between charged and actual back into the bucket. Reconciliation SHALL happen exactly once per charged request — on success, on error, and at the end of a streamed response. When actual usage never becomes known, the full charge SHALL stand. Under sustained overload, the tokens actually consumed by admitted requests SHALL stay within the bucket bound over the window, and the excess at any instant SHALL be bounded by the reservation error of requests admitted before reconciliation caught up — at most the burst capacity times the ratio of under-reservation; a tenant that over-reserves SHALL show no excess. This SHALL be measurable by one reproducible command whose results are published with their protocol.

#### Scenario: Token budget exhausted

- **WHEN** a tenant's token bucket cannot cover a request's input estimate plus output reservation
- **THEN** the request is rejected with 429 `rate_limited`, a positive `retry_after_ms`, and no provider is called

#### Scenario: Reservation larger than the bucket

- **WHEN** a request's input estimate plus `max_tokens` exceeds the token bucket's capacity
- **THEN** the gateway responds 400 `invalid_request`, charges nothing, and calls no provider

#### Scenario: Default reservation must fit the bucket

- **WHEN** the configured default reservation exceeds the token burst while token limiting is on
- **THEN** the gateway fails to start with a configuration error

#### Scenario: Unused reservation is returned

- **WHEN** a completion reserves more output tokens than the provider actually generated
- **THEN** the difference is returned to the tenant's bucket after completion, and available tokens never exceed the bucket capacity

#### Scenario: Streamed responses reconcile at stream end

- **WHEN** a streamed completion delivers its usage in the stream
- **THEN** reconciliation runs when the stream ends, using that usage

#### Scenario: Unknown usage keeps the charge

- **WHEN** a request's actual usage never becomes known
- **THEN** the original charge stands and nothing is refunded

#### Scenario: Token limiting disabled explicitly

- **WHEN** the token limit is configured as zero
- **THEN** no request is charged or rejected by the token limit, request limiting still applies, and the gateway logs a warning at startup that token limiting is disabled

#### Scenario: Token consumption under overload stays within the bound, measured

- **WHEN** a tenant with a full bucket of capacity B and refill r offers requests above its token rate for a window T against an upstream with a fixed usage per request, charging less than that usage on admission — and, in a second arm, charging more
- **THEN** the tokens consumed over the window are at most `B + r·(T + ε)`, the peak excess in the under-reserving arm is at most `B × (actual − charged) / charged`, the over-reserving arm shows no excess, and no reconciliation fails

### Requirement: Request limits and token limits are independent dimensions

RPM and TPM SHALL be separate buckets per tenant: exhausting one dimension SHALL NOT consume or block the other beyond rejecting the requests it governs.

#### Scenario: Token rejection does not consume request quota beyond the attempt

- **WHEN** a tenant is rejected by the token limit
- **THEN** subsequent small requests within both limits are admitted

### Requirement: Lease mode trades per-window exactness for fewer store round-trips

With `PUSAQ_RATELIMIT_MODE=lease`, each gateway replica SHALL take up to K request tokens (`PUSAQ_LEASE_SIZE`) from a tenant's bucket in one atomic store operation and admit up to K requests locally without consulting the store. Cumulative admissions from a full bucket SHALL never exceed the bucket capacity plus accrued refill. Within any window, the admitted count MAY fall short of strict accounting by at most `M·K` tokens across M replicas (tokens taken but not yet spent) and SHALL never exceed it. Tokens unused when a lease expires (`PUSAQ_LEASE_TTL`) or when the replica drains SHALL be returned to the shared bucket; a replica that stops without draining loses at most K tokens per tenant, which under-admits and never overruns. A store denial SHALL be cached locally until the store-computed retry time. Lease mode SHALL apply to request limits only; token limits SHALL remain strict. The mode SHALL be announced at startup and its configuration validated.

#### Scenario: Lease acquired once per K requests

- **WHEN** a tenant sends N requests to one replica in lease mode with lease size K and enough capacity
- **THEN** all N are admitted and the replica makes ⌈N/K⌉ store round-trips

#### Scenario: Cumulative admissions never exceed the bucket

- **WHEN** a tenant sends more requests than its bucket holds through any number of replicas in lease mode, with no refill accruing
- **THEN** the total admitted equals the bucket capacity and every further request is rejected

#### Scenario: Under-admission bounded by M·K

- **WHEN** demand stops while M replicas hold leases
- **THEN** the tokens not yet admitted across all replicas number at most M·K, and after every replica drains the store holds exactly the tokens that were never admitted

#### Scenario: Expired lease tokens are returned

- **WHEN** a replica's lease for a tenant passes its TTL with tokens unspent
- **THEN** those tokens are returned to the shared bucket and become available to every replica

#### Scenario: Graceful shutdown returns held tokens

- **WHEN** a replica in lease mode drains
- **THEN** every token it still holds is returned to the shared bucket before it exits

#### Scenario: Bucket with fewer than K tokens grants what it has

- **WHEN** a lease is requested from a bucket holding fewer than K but at least one whole token
- **THEN** the replica receives the whole tokens available, and the request that triggered the take is admitted

#### Scenario: Denial cached until retry

- **WHEN** the store denies a lease with a retry time
- **THEN** the replica rejects that tenant's requests locally until the retry time passes, making no further store round-trip for them

#### Scenario: Mode is explicit at startup

- **WHEN** the gateway starts with rate limiting enabled
- **THEN** the startup log names the mode, and in lease mode the lease size and TTL

#### Scenario: Lease configuration is validated

- **WHEN** the mode is neither `strict` nor `lease`, the lease size is not a positive integer, exceeds the request bucket capacity, or the lease TTL is not positive
- **THEN** the gateway fails to start with a configuration error
