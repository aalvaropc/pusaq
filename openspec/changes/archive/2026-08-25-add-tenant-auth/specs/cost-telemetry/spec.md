# cost-telemetry

## MODIFIED Requirements

### Requirement: Cost events carry the accounting fields

Every cost event SHALL include at least: request ID, timestamp, operation, tenant, outcome (success, error code, or panic), and total latency. For requests that resolve a model, the event SHALL additionally include the requested model (alias) and the served model, and — when the provider reports usage — prompt and completion token counts. The schema SHALL remain extensible so other changes can add price fields without breaking consumers.

#### Scenario: Minimum fields present

- **WHEN** any cost event is emitted
- **THEN** it contains request ID, timestamp, operation, tenant, outcome, and latency

#### Scenario: Completion events carry model and usage

- **WHEN** a chat-completions request succeeds
- **THEN** its cost event contains the requested alias, the served model, and the provider-reported prompt and completion token counts
