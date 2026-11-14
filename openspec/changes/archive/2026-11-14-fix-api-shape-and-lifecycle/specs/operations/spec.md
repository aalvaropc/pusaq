## MODIFIED Requirements

### Requirement: Readiness endpoint backed by a controllable flag

The gateway SHALL expose `GET /readyz` returning 200 when ready to accept traffic and 503 when not. Readiness SHALL be an internal flag that the process itself flips (during startup and drain). For failure-injection experiments, the gateway SHALL additionally serve — only when an admin token is configured — `POST /admin/readiness` guarded by that bearer token, with the scheme matched as for tenant authentication: `state=down&for=<duration>` opens a not-ready window of at most one hour during which `/readyz` returns 503 and new requests are refused with 503 `not_ready` and a `Retry-After` for the window's remainder, while requests and streams already in flight complete normally; `state=up` ends the window. A refusal SHALL happen after authentication and before the request body is read or any request or token limit is charged; it SHALL carry a request ID, and exactly one cost event SHALL record it with code `not_ready` and no run ID. Each action SHALL be logged as one `readiness_window` record. Draining SHALL keep serving new requests during the drain delay, as before.

#### Scenario: Ready while serving

- **WHEN** the gateway has finished startup and is not draining
- **THEN** `GET /readyz` returns 200

#### Scenario: Not ready while draining

- **WHEN** the gateway is draining
- **THEN** `GET /readyz` returns 503 while in-flight requests continue to be served

#### Scenario: Injected not-ready window refuses new requests

- **WHEN** an authorized `POST /admin/readiness?state=down&for=30s` is received
- **THEN** `GET /readyz` returns 503, a new authenticated completion request is refused with 503 `not_ready`, a `Retry-After` no larger than the remaining window and a request ID, exactly one cost event records `not_ready` with that ID and no run ID, no request or token quota is charged, and after the window both return to normal

#### Scenario: Unauthenticated request during a window

- **WHEN** API keys are configured and a request with a missing or unknown key arrives during an injected window
- **THEN** the response is 401 `unauthorized`, as outside the window

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

On SIGTERM or SIGINT, the gateway SHALL first turn not-ready, then allow in-flight requests to complete within a configurable grace period, then flush telemetry and exit. Requests still in flight when the signal arrives SHALL NOT be cut before the grace period expires. Once the first signal is received, a second SIGTERM or SIGINT SHALL terminate the process immediately with the signal's default action, without waiting for the drain delay or the grace period; requests still in flight are then cut and buffered cost events can be lost.

#### Scenario: In-flight requests complete during drain

- **WHEN** SIGTERM arrives while a request is in flight
- **THEN** readiness turns 503, the in-flight request completes normally, and the process exits within the grace period

#### Scenario: Grace period bounds shutdown

- **WHEN** an in-flight request outlives the configured grace period during drain
- **THEN** the process still exits at the end of the grace period

#### Scenario: Second signal forces the exit

- **WHEN** a second SIGTERM arrives while the gateway is draining
- **THEN** the process is terminated by that signal at once, before the drain delay ends
