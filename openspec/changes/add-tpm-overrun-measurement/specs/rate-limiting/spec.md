# rate-limiting

## MODIFIED Requirements

### Requirement: Token-based limits with reservation and reconciliation

With rate limiting enabled, the gateway SHALL additionally limit each tenant by LLM tokens per minute unless token limiting is explicitly disabled by configuration. After the request's model is resolved — and only then — the gateway SHALL atomically charge the tenant's token bucket with the request's input-token estimate plus an output reservation (the request's `max_tokens` when present, a configured default otherwise). When the request's actual usage becomes known, the gateway SHALL atomically reconcile the difference between charged and actual back into the bucket. Reconciliation SHALL happen exactly once per charged request — on success, on error, and at the end of a streamed response. When actual usage never becomes known, the full charge SHALL stand. Under sustained overload, the tokens actually consumed by admitted requests SHALL stay within the bucket bound over the window, and the excess at any instant SHALL be bounded by the reservation error of requests admitted before reconciliation caught up — at most the burst capacity times the ratio of under-reservation; a tenant that over-reserves SHALL show no excess. This SHALL be measurable by one reproducible command whose results are published with their protocol.

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

#### Scenario: Token consumption under overload stays within the bound, measured

- **WHEN** a tenant with a full bucket of capacity B and refill r offers requests above its token rate for a window T against an upstream with a fixed usage per request, charging less than that usage on admission — and, in a second arm, charging more
- **THEN** the tokens consumed over the window are at most `B + r·(T + ε)`, the peak excess in the under-reserving arm is at most `B × (actual − charged) / charged`, the over-reserving arm shows no excess, and no reconciliation fails
