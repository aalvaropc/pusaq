# operations

## MODIFIED Requirements

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
