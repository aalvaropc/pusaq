## ADDED Requirements

### Requirement: Limiter measurement sources agree

The limiter measurement verifier SHALL compare client admissions, rejections and unexpected outcomes with the gateway's cost events for the same run and tenant, and compare admitted requests with the mock provider's served count. Missing, duplicated or differently classified evidence SHALL fail verification.

#### Scenario: Independent counters agree

- **WHEN** the client, gateway and provider evidence agree within the configured admission bounds
- **THEN** verification succeeds

#### Scenario: Gateway evidence differs

- **WHEN** a gateway event is missing, duplicated or classified differently from the client count
- **THEN** verification fails even when client admissions remain within their bounds
