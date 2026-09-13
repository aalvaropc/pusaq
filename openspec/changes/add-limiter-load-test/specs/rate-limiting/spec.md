# rate-limiting

## MODIFIED Requirements

### Requirement: The limit is exact across replicas

The admission decision SHALL be atomic in the shared store: under concurrent requests from any number of gateway replicas, the number admitted within a window SHALL NOT exceed the bucket capacity plus accrued refill — never a multiple of it. This property SHALL be verifiable against real gateway processes by one reproducible command whose results are published together with the protocol that produced them.

#### Scenario: Concurrent replicas cannot overrun

- **WHEN** many concurrent requests race through multiple gateway instances sharing one store, with no refill accruing
- **THEN** exactly the bucket capacity is admitted and everything beyond is rejected

#### Scenario: Exactness holds across process replicas under sustained overload

- **WHEN** two gateway processes behind a load balancer receive requests from one tenant at a multiple of its sustained rate for a duration T, starting from a full bucket of capacity B with refill rate r
- **THEN** the admitted count lies within `[B + r·(T − ε), B + r·(T + ε)]` for a small declared tolerance ε, every other request is rejected with 429 `rate_limited`, the upstream's served count equals the admitted count, and no request was admitted by fail-open

#### Scenario: A co-tenant is unaffected during another tenant's overload

- **WHEN** a second tenant sends requests well under its own limit while the first tenant is overloading its bucket
- **THEN** the second tenant receives no rejections

### Requirement: Token-based limits with reservation and reconciliation

With rate limiting enabled, the gateway SHALL additionally limit each tenant by LLM tokens per minute unless token limiting is explicitly disabled by configuration. After the request's model is resolved — and only then — the gateway SHALL atomically charge the tenant's token bucket with the request's input-token estimate plus an output reservation (the request's `max_tokens` when present, a configured default otherwise). When the request's actual usage becomes known, the gateway SHALL atomically reconcile the difference between charged and actual back into the bucket. Reconciliation SHALL happen exactly once per charged request — on success, on error, and at the end of a streamed response. When actual usage never becomes known, the full charge SHALL stand.

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

#### Scenario: Token limiting disabled explicitly

- **WHEN** the token limit is configured as zero
- **THEN** no request is charged or rejected by the token limit, request limiting still applies, and the gateway logs a warning at startup that token limiting is disabled
