## ADDED Requirements

### Requirement: Chaos evidence agrees by tenant and region

The chaos verifier SHALL compare successful main requests including setup and retries, split-tenant admissions and rate-limit rejections, and completed streams against gateway events by region. Local comparisons SHALL require equality after draining; the existing cloud tolerance of five events SHALL apply only to the total main successes. Platform, transport and pre-admission refusals without gateway events SHALL be reported separately rather than treated as missing cost events. Inconsistent comparable evidence SHALL fail the run.

#### Scenario: Local outcomes agree

- **WHEN** a local chaos run finishes and its drained gateway evidence agrees with comparable client outcomes by tenant and region
- **THEN** verification succeeds

#### Scenario: Regional evidence is missing or duplicated

- **WHEN** a split admission, split rejection or completed-stream event differs from the client count in a region
- **THEN** verification fails even if total main successes agree

#### Scenario: Request never enters the pipeline

- **WHEN** a request fails in transport, at the platform or at the injected pre-admission readiness check
- **THEN** the report retains that outcome without requiring a cost event for that request
