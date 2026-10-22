## MODIFIED Requirements

### Requirement: Cost events carry the accounting fields

Every cost event SHALL include at least: request ID, timestamp, operation, tenant, outcome (success, error code, or panic), and total latency. For requests that resolve a model, the event SHALL additionally include the requested model (alias) and the served model, and — when the provider reports usage — prompt and completion token counts. When the replica is labeled with a region, every event SHALL carry it; when the authenticated payload declares a valid run id, the event SHALL carry it — so a multi-region measurement run can be read back per region and per run from the event stream alone. The schema SHALL remain extensible so price fields can be added without breaking consumers.

#### Scenario: Minimum fields present

- **WHEN** any cost event is emitted
- **THEN** it contains request ID, timestamp, operation, tenant, outcome, and latency

#### Scenario: Completion events carry model and usage

- **WHEN** a chat-completions request succeeds
- **THEN** its cost event contains the requested alias, the served model, and the provider-reported prompt and completion token counts

#### Scenario: Region and run id present

- **WHEN** a replica labeled `us-east1` serves a request whose `metadata.run_id` is `r1`, and the request fails after parsing
- **THEN** its cost event carries `region` `us-east1` and `run_id` `r1`

