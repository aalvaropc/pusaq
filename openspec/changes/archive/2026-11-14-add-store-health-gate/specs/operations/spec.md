## MODIFIED Requirements

### Requirement: Event report views

The report SHALL contain six views: requests, tokens and USD cost per tenant with estimated cost apart from measured cost; latency p50, p95 and p99 of successful requests per route by the nearest-rank method; outcomes by error code and HTTP status; fallback and breaker activity; request-limit and token-limit denials with fail-open admissions by decision from the cost event's `fail_open` field, and the store health gate's transitions from `breaker_state` records with `component` `store`; and the shadow cache's predicted hit-rate next to its false-positive upper bound. Store-gate transitions SHALL NOT be counted as backend breaker transitions. A view without supporting records SHALL say so instead of showing numbers. The report SHALL state that its figures describe its input only.

#### Scenario: Cost per tenant separates estimated cost

- **WHEN** the input holds priced events for two tenants, one of them carrying `cost_estimated`
- **THEN** each tenant's requests, tokens and measured cost are summed, and the estimated cost is reported in its own column and not in the measured total

#### Scenario: Nearest-rank latency percentiles

- **WHEN** a route has successful events with latencies 1 to 100 ms and other routes or failed requests have different latencies
- **THEN** that route reports p50 50 ms, p95 95 ms and p99 99 ms, computed from its own successful events only

#### Scenario: Limit kind from the event

- **WHEN** one `rate_limited` event has no `requested_model` and another has one
- **THEN** the first counts as a request-limit denial and the second as a token-limit denial

#### Scenario: Fail-open admissions from the event field

- **WHEN** selected events carry `fail_open` values `rpm`, `tpm` and `budget`, and the input also holds `failing open` warning lines
- **THEN** each value counts once as a fail-open admission of its decision for the event's tenant, and the warning lines add nothing

#### Scenario: Store-gate transitions in the limits view

- **WHEN** the input holds `breaker_state` records for a backend and for `component` `store`
- **THEN** the store records are listed with their time, replica and states in the rate-limiting view, and only the backend records appear among the breaker transitions

#### Scenario: Fallback against the first choice

- **WHEN** a selected request has a breaker-skip or failed-attempt record and was served
- **THEN** it counts as served after fallback, the first named backend is its first choice, and the backend's skips and failed attempts are counted
