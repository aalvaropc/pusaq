# cost-telemetry

## ADDED Requirements

### Requirement: Completion events record tokens charged

For requests charged against a token limit, the cost event SHALL record the tokens charged (input estimate plus output reservation) alongside the actual usage, so reservation error is measurable from the event stream alone.

#### Scenario: Charged and actual both present

- **WHEN** a completion charged against the token bucket finishes with known usage
- **THEN** its cost event contains both the charged tokens and the actual prompt/completion counts
