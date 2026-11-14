## MODIFIED Requirements

### Requirement: Fail-closed store behavior

A tenant's policy MAY set `fail_closed`. For such a tenant, a store failure while deciding the request limit, charging the token limit, or reading the budget SHALL reject the request with 503 `store_unavailable`, calling no provider; tenants without the flag SHALL keep the default fail-open behavior. A store call skipped by the instance's open store health gate is a store failure: a fail-closed tenant's request SHALL be rejected without waiting for the store. Token reconciliation is unaffected.

#### Scenario: Request limit store failure rejects

- **WHEN** the store is unreachable when a fail-closed tenant's request is rate-limited
- **THEN** the response is 503 `store_unavailable` and the degradation is logged

#### Scenario: Token charge store failure rejects

- **WHEN** the store is unreachable when a fail-closed tenant's request is charged tokens
- **THEN** the response is 503 `store_unavailable` and no provider is called

#### Scenario: Budget read failure rejects

- **WHEN** the store is unreachable when a fail-closed tenant's budget is read
- **THEN** the response is 503 `store_unavailable`

#### Scenario: Open gate rejects at once

- **WHEN** the store health gate is open and a fail-closed tenant's request arrives
- **THEN** the response is 503 `store_unavailable` without any store call, no provider is called, and the degradation is logged

#### Scenario: Tenants without the flag still fail open

- **WHEN** the store is unreachable for a tenant whose policy does not set `fail_closed`
- **THEN** the request is admitted and the degradation is logged
