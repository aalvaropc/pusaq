## MODIFIED Requirements

### Requirement: Regional failover under load is measured, not asserted

Regional failover SHALL be measurable by one reproducible command against the runtime deployment: sustained traffic through the balancer while one region's readiness is failed on purpose at a known instant, never by deleting a service. The run SHALL report separately, each from a stated source: the time until new requests are answered by another region, the longest interval without a success, first-attempt failures and how many recovered by one retry, streams complete / interrupted / never started / refused, latency before / during / after per region, and — for a tenant whose quota is split across regions — admissions per region. A stream SHALL count as interrupted only when its response started and ended without `[DONE]`; a stream that fails before its first response byte SHALL count as never started and SHALL be retried once. Times SHALL be measured from the send instant the client records before each request, and latency percentiles SHALL interpolate linearly between the closest ranks. The results SHALL be published with their protocol, including numbers that disappoint, and SHALL never be summarized as "zero dropped streams" unless the count was zero and the failure mode is stated.

#### Scenario: Readiness of one region failed under load

- **WHEN** the run fails region A's readiness for a window while traffic flows through the balancer
- **THEN** the report states the time to the other region, the longest gap, the failure / recovery counts, the stream counts, the per-region latency, and the split tenant's admissions, each cross-checked against the gateways' events

#### Scenario: Control run

- **WHEN** the same load runs with no injection
- **THEN** the report gives the baseline latency per region and shows no failures beyond rate limiting of the split tenant

#### Scenario: Stream fails before its first byte

- **WHEN** a streamed request fails in transport before any response byte arrives
- **THEN** it is reported as never started, not interrupted, and the outcome of its one retry is reported

#### Scenario: Connection set-up precedes the request

- **WHEN** a request spends time connecting before its bytes are written
- **THEN** its time is measured from the instant the client started the request, not from completion minus the request duration

## ADDED Requirements

### Requirement: Chaos evidence is scoped to its run

Gateway records that carry no run id — readiness-window records and fail-open warnings — SHALL count for a chaos run only when logged between the run's start and end on the client clock, widened by a stated clock slack. An injected run SHALL contain exactly one readiness-window down record for the failed region, and a control run none. Cloud collection SHALL poll the log store until it returns a cost event for every client attempt a gateway answered past readiness and, in an injected run, the failed region's down record, for at most a stated wait; it SHALL then verify whatever arrived.

#### Scenario: Record from another run

- **WHEN** the gateway logs hold a readiness-window record or fail-open warning logged outside the run's interval
- **THEN** collection excludes it and verification judges the run on its own records

#### Scenario: Duplicate readiness window

- **WHEN** an injected run's records contain more than one down record for the failed region
- **THEN** verification fails

#### Scenario: Cloud logs arrive with a delay

- **WHEN** Cloud Logging returns the run's records only after several polls
- **THEN** collection waits for them within the stated bound and verification uses them

#### Scenario: Cloud logs stay incomplete

- **WHEN** the bound expires before the expected records arrive
- **THEN** verification runs on the records that arrived and fails on the missing evidence
