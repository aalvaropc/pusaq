## ADDED Requirements

### Requirement: Store outage is measured per arm

A limit-store outage SHALL be measurable by one reproducible local command: steady traffic from a fail-open tenant with request and token limits, a budgeted tenant and a fail-closed tenant, while the store is made unavailable on purpose for a fixed window in two separate arms — hung (the store process frozen, calls wait for the limiter budget) and down (the store container stopped) — and then restored. For each arm and each period (before, during and after the window, assigned by the instant the client sent each request), the run SHALL report separately per tenant: requests admitted, limited, refused with `store_unavailable`, other gateway 5xx and transport failures; latency p50/p95/p99 interpolated between the closest ranks; and fail-open admissions by decision from the cost event's `fail_open` field. Per arm it SHALL also report the fail-open tenant's admissions during the window above its configured rate, the budget increments the gateway reported lost with the store counter's value, and the time from the restore until the last request that met a store failure. Every answered request SHALL match exactly one cost event of its run with the same tenant and classification; fail-open entries SHALL match the gateway's fail-open warnings; and the store's spend counter SHALL lie between the summed cost minus the reported lost increments and the summed cost. The results SHALL be published with their protocol and SHALL NOT be described as globally exact limits.

#### Scenario: Store hung under load

- **WHEN** the store process is frozen for the window while the three tenants send traffic
- **THEN** the report gives each period's outcomes and latency per tenant, the fail-open admissions by decision, the overrun over the configured rate, the lost and late-applied budget increments and the recovery time, cross-checked against the gateways' events

#### Scenario: Store down under load

- **WHEN** the store container is stopped for the window and started again
- **THEN** the same numbers are reported for that arm, separately from the hung arm

#### Scenario: Request and event disagree

- **WHEN** an answered request has no cost event, two cost events, or an event with another tenant or classification, or an event has no answered request
- **THEN** verification fails

#### Scenario: Injection without effect

- **WHEN** no request sent during the window met a store failure, or a request sent before it did
- **THEN** verification fails

#### Scenario: Policy violated during the outage

- **WHEN** a fail-closed tenant's event carries `fail_open`, or a fail-open tenant is refused with `store_unavailable`
- **THEN** verification fails

#### Scenario: Spend counter outside its bounds

- **WHEN** the store's spend counter is below the summed cost minus the reported lost increments, or above the summed cost
- **THEN** verification fails
