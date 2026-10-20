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
