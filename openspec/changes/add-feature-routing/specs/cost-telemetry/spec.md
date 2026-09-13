# cost-telemetry

## ADDED Requirements

### Requirement: Events record the feature tag and routing reason

For requests that resolve a model, the cost event SHALL record how the requested alias was resolved (`route_reason`, using the same vocabulary as the response header) and, when the request declared one, its feature tag — for any alias, not only `pusaq/auto` — so cost can be attributed per feature and per routing decision from the event stream alone.

#### Scenario: Tagged auto request

- **WHEN** a request for `pusaq/auto` with a feature tag is served
- **THEN** its cost event carries the tag and the reason `rule:<tag>` or a default reason

#### Scenario: Direct alias carries the tag and reason direct

- **WHEN** a request for a concrete alias declares a feature tag
- **THEN** its cost event carries that tag and the reason `direct`
