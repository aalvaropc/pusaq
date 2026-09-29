# rate-limiting

## MODIFIED Requirements

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
