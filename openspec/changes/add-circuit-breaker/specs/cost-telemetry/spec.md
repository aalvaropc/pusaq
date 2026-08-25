# cost-telemetry

## ADDED Requirements

### Requirement: Events record the serving backend and attempts

For requests that reach the routing flow, the cost event SHALL record which upstream backend actually served the request and how many backend attempts were made, so failover is observable from the event stream alone.

#### Scenario: Failover visible in the event

- **WHEN** a request is served by a fallback backend after the primary failed
- **THEN** its cost event names the fallback backend and an attempt count greater than one
