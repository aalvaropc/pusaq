# operations Specification

## Purpose
Liveness, readiness, and graceful drain behavior of the gateway process — the contract load balancers and chaos experiments rely on. The constitution requires failure injection through the readiness endpoint, so readiness is controllable from day one.

## Requirements

### Requirement: Liveness endpoint

The gateway SHALL expose `GET /healthz` returning HTTP 200 while the process is running. Liveness SHALL NOT depend on downstream dependencies.

#### Scenario: Liveness while running

- **WHEN** the gateway process is up
- **THEN** `GET /healthz` returns 200

### Requirement: Readiness endpoint backed by a controllable flag

The gateway SHALL expose `GET /readyz` returning 200 when ready to accept traffic and 503 when not. Readiness SHALL be an internal flag that the process itself flips (during startup and drain). For failure-injection experiments, the gateway SHALL additionally serve — only when an admin token is configured — `POST /admin/readiness` guarded by that bearer token: `state=down&for=<duration>` opens a not-ready window of at most one hour during which `/readyz` returns 503 and new requests are refused with 503 `not_ready` and a `Retry-After` for the window's remainder, while requests and streams already in flight complete normally; `state=up` ends the window. Each action SHALL be logged as one `readiness_window` record. Draining SHALL keep serving new requests during the drain delay, as before.

#### Scenario: Ready while serving

- **WHEN** the gateway has finished startup and is not draining
- **THEN** `GET /readyz` returns 200

#### Scenario: Not ready while draining

- **WHEN** the gateway is draining
- **THEN** `GET /readyz` returns 503 while in-flight requests continue to be served

#### Scenario: Injected not-ready window refuses new requests

- **WHEN** an authorized `POST /admin/readiness?state=down&for=30s` is received
- **THEN** `GET /readyz` returns 503, a new completion request is refused with 503 `not_ready` and a `Retry-After` no larger than the remaining window, no cost event is emitted for it, and after the window both return to normal

#### Scenario: In-flight streams outlive the injection

- **WHEN** a streamed response is being delivered and a not-ready window is opened
- **THEN** the stream completes and its cost event records success; the next request is refused

#### Scenario: Window ended early

- **WHEN** an authorized `POST /admin/readiness?state=up` is received during a window
- **THEN** readiness returns 200 and new requests are served immediately

#### Scenario: Admin endpoint guarded

- **WHEN** no admin token is configured, or the request carries a missing or wrong token
- **THEN** the endpoint is absent (404) or answers 401 `unauthorized`, and readiness is unchanged

### Requirement: Graceful drain on termination signal

On SIGTERM or SIGINT, the gateway SHALL first turn not-ready, then allow in-flight requests to complete within a configurable grace period, then flush telemetry and exit. Requests still in flight when the signal arrives SHALL NOT be cut before the grace period expires.

#### Scenario: In-flight requests complete during drain

- **WHEN** SIGTERM arrives while a request is in flight
- **THEN** readiness turns 503, the in-flight request completes normally, and the process exits within the grace period

#### Scenario: Grace period bounds shutdown

- **WHEN** an in-flight request outlives the configured grace period during drain
- **THEN** the process still exits at the end of the grace period

### Requirement: Development ports are local to the host

The default Compose development stack SHALL publish the gateway, Redis and mock-provider ports only on IPv4 loopback, preserving their existing host ports and service-to-service connectivity.

#### Scenario: Default host port bindings

- **WHEN** the default Compose configuration is resolved
- **THEN** every published service port binds to `127.0.0.1` and none binds to all interfaces

#### Scenario: Local completion and fallback

- **WHEN** the development stack is running and the primary mock is failed through its admin endpoint
- **THEN** a completion sent from the host reaches the fallback and returns successfully

### Requirement: Failed measurements retain verification

Local load, breaker, TPM and chaos commands SHALL attempt their evidence collection and verification after k6 finishes, including when k6 fails. They SHALL use one stable run id. A k6 failure SHALL retain its exit code; otherwise the first collection or verification failure SHALL determine the exit status.

#### Scenario: Client threshold fails

- **WHEN** k6 exits with a failing threshold
- **THEN** evidence collection and verification are attempted and the command exits with the k6 failure

#### Scenario: Evidence collection fails

- **WHEN** k6 succeeds but evidence collection fails
- **THEN** verification is attempted and the command exits with the collection failure

#### Scenario: Verifier detects inconsistent evidence

- **WHEN** execution and collection succeed but verification fails
- **THEN** the command exits with the verification failure

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

#### Scenario: One response fingerprint makes the bound uninformative

- **WHEN** every selected `cache_shadow` record carries the same response fingerprint, as the mock provider's canned answer produces
- **THEN** the report and the summary state that the false-positive bound is zero by construction and cannot detect false positives in that input

### Requirement: Local dashboard target

`make dashboard` SHALL collect the gateway records from the local Compose stack, or read an existing log file when one is given, and write the report under an ignored output directory, without cloud resources.

#### Scenario: Report from the running stack

- **WHEN** the local stack has served completions and `make dashboard` runs
- **THEN** an HTML report and its text summary are written under `loadtest/out/` from the stack's own records
