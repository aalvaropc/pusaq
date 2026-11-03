## ADDED Requirements

### Requirement: Event report from gateway log lines

The repository SHALL provide a command that reads the gateway's JSON log lines from a file or standard input and writes one self-contained HTML report — inline styles and graphics, no scripts and no external resources — plus a short text summary on standard output. Lines that are not gateway records, malformed records and duplicate cost events SHALL be skipped and counted. When a run id is given, only cost events with that `run_id` SHALL be selected, and records keyed by request id SHALL count only when their request's cost event is selected. The command SHALL exit with an error when the selection holds no cost events.

#### Scenario: Run filter and non-event lines

- **WHEN** the input mixes events of two runs with plain-text lines, malformed JSON and records of other kinds, and a run id is given
- **THEN** only that run's events and the records joined to them are aggregated, and the skipped lines are counted in the summary

#### Scenario: Empty selection fails

- **WHEN** the input or the selected run holds no cost events
- **THEN** the command exits with an error and writes no report

### Requirement: Event report views

The report SHALL contain six views: requests, tokens and USD cost per tenant with estimated cost apart from measured cost; latency p50, p95 and p99 of successful requests per route by the nearest-rank method; outcomes by error code and HTTP status; fallback and breaker activity; request-limit and token-limit denials with fail-open admissions; and the shadow cache's predicted hit-rate next to its false-positive upper bound. A view without supporting records SHALL say so instead of showing numbers. The report SHALL state that its figures describe its input only.

#### Scenario: Cost per tenant separates estimated cost

- **WHEN** the input holds priced events for two tenants, one of them carrying `cost_estimated`
- **THEN** each tenant's requests, tokens and measured cost are summed, and the estimated cost is reported in its own column and not in the measured total

#### Scenario: Nearest-rank latency percentiles

- **WHEN** a route has successful events with latencies 1 to 100 ms and other routes or failed requests have different latencies
- **THEN** that route reports p50 50 ms, p95 95 ms and p99 99 ms, computed from its own successful events only

#### Scenario: Limit kind from the event

- **WHEN** one `rate_limited` event has no `requested_model` and another has one
- **THEN** the first counts as a request-limit denial and the second as a token-limit denial, and `failing open` warnings for selected requests count as fail-open admissions by store

#### Scenario: Fallback against the first choice

- **WHEN** a selected request has a breaker-skip or failed-attempt record and was served
- **THEN** it counts as served after fallback, the first named backend is its first choice, and the backend's skips and failed attempts are counted

### Requirement: Shadow hit-rate is paired with its false-positive bound

The shadow cache view SHALL show the predicted hit-rate — records labeled `shadow_hit` over eligible successful completions — only together with the false-positive upper bound: the share of predicted hits whose neighbor fingerprint differs from the response fingerprint. A τ sweep over `best_similarity` SHALL show both numbers for every τ, and the similarity distribution SHALL split records by fingerprint agreement.

#### Scenario: Hit-rate paired with its false-positive bound

- **WHEN** the input holds eligible completions and `cache_shadow` records, some predicted hits with differing fingerprints
- **THEN** the shadow view shows the predicted hit-rate over eligible completions together with the share of predicted hits whose fingerprints differ, at the configured τ and at each τ of the sweep

### Requirement: Local dashboard target

`make dashboard` SHALL collect the gateway records from the local Compose stack, or read an existing log file when one is given, and write the report under an ignored output directory, without cloud resources.

#### Scenario: Report from the running stack

- **WHEN** the local stack has served completions and `make dashboard` runs
- **THEN** an HTML report and its text summary are written under `loadtest/out/` from the stack's own records
