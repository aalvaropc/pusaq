# resilience

## ADDED Requirements

### Requirement: Regional failover under load is measured, not asserted

Regional failover SHALL be measurable by one reproducible command against the runtime deployment: sustained traffic through the balancer while one region's readiness is failed on purpose at a known instant, never by deleting a service. The run SHALL report separately, each from a stated source: the time until new requests are answered by another region, the longest interval without a success, first-attempt failures and how many recovered by one retry, streams complete / interrupted / refused, latency before / during / after per region, and — for a tenant whose quota is split across regions — admissions per region. The results SHALL be published with their protocol, including numbers that disappoint, and SHALL never be summarized as "zero dropped streams" unless the count was zero and the failure mode is stated.

#### Scenario: Readiness of one region failed under load

- **WHEN** the run fails region A's readiness for a window while traffic flows through the balancer
- **THEN** the report states the time to the other region, the longest gap, the failure / recovery counts, the stream counts, the per-region latency, and the split tenant's admissions, each cross-checked against the gateways' events

#### Scenario: Control run

- **WHEN** the same load runs with no injection
- **THEN** the report gives the baseline latency per region and shows no failures beyond rate limiting of the split tenant
