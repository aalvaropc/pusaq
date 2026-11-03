## MODIFIED Requirements

### Requirement: Cost events carry the accounting fields

Every cost event SHALL include at least: request ID, timestamp, operation, tenant, outcome (success, error code, or panic), and total latency in whole milliseconds, the same unit in every encoding of the event. For requests that resolve a model, the event SHALL additionally include the requested model (alias) and the served model, and — when the provider reports usage — prompt and completion token counts. When the replica is labeled with a region, every event SHALL carry it; when the authenticated payload declares a valid run id, the event SHALL carry it — so a multi-region measurement run can be read back per region and per run from the event stream alone. The schema SHALL remain extensible so price fields can be added without breaking consumers.

#### Scenario: Minimum fields present

- **WHEN** any cost event is emitted
- **THEN** it contains request ID, timestamp, operation, tenant, outcome, and latency

#### Scenario: Completion events carry model and usage

- **WHEN** a chat-completions request succeeds
- **THEN** its cost event contains the requested alias, the served model, and the provider-reported prompt and completion token counts

#### Scenario: Region and run id present

- **WHEN** a replica labeled `us-east1` serves a request whose `metadata.run_id` is `r1`, and the request fails after parsing
- **THEN** its cost event carries `region` `us-east1` and `run_id` `r1`

#### Scenario: Latency is milliseconds in every encoding

- **WHEN** a request that took 1.5 s is encoded as JSON and written by the log sink
- **THEN** both carry `latency_ms` 1500

### Requirement: Emitter shuts down cleanly

On gateway shutdown, the emitter SHALL stop accepting new events and flush already-buffered events to the sink before the process exits. Every event the emitter accepted before shutdown SHALL reach the sink; an event offered while shutdown is in progress SHALL either reach the sink or be counted as dropped, never neither.

#### Scenario: Buffered events survive shutdown

- **WHEN** the gateway begins graceful shutdown while events sit in the buffer
- **THEN** those events are delivered to the sink before the process exits

#### Scenario: Events racing shutdown are accounted for

- **WHEN** events are emitted concurrently with the emitter's shutdown
- **THEN** after shutdown, delivered plus dropped events equal the events emitted

### Requirement: Events record the serving backend and attempts

Every cost event SHALL record how many backend attempts were made, zero when none, and, when a backend answered, which upstream backend served the request, so failover is observable from the event stream alone — including for requests that exhausted the fallback chain.

#### Scenario: Failover visible in the event

- **WHEN** a request is served by a fallback backend after the primary failed
- **THEN** its cost event names the fallback backend and an attempt count greater than one

#### Scenario: Exhausted chain records its attempts

- **WHEN** every backend of a two-backend chain fails
- **THEN** its cost event carries `attempts` 2 and no served backend

## ADDED Requirements

### Requirement: Events record fail-open admissions

When a store failure lets a request through a decision it was configured for — the request limit, the token charge, or the budget read — instead of making that decision, the cost event SHALL name each such decision in `fail_open` (`rpm`, `tpm`, `budget`). An event without `fail_open` SHALL mean no decision failed open, so a request whose token charge failed open is distinguishable from one with token limiting disabled.

#### Scenario: Token charge fails open

- **WHEN** the token limiter's store is unavailable and the tenant is not fail-closed
- **THEN** the request is served, its cost event carries `fail_open` `tpm` and no `tokens_charged`

#### Scenario: Request limit and budget read fail open

- **WHEN** the request limiter's store or a budgeted tenant's spend store is unavailable and the tenant is not fail-closed
- **THEN** the cost event carries `fail_open` `rpm` or `budget` respectively

#### Scenario: Store answers

- **WHEN** every configured store decision is made
- **THEN** the cost event carries no `fail_open`
