# rate-limiting

## ADDED Requirements

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
