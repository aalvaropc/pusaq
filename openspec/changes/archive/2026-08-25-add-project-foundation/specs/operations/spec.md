# operations

## Purpose

Liveness, readiness, and graceful drain behavior of the gateway process — the contract load balancers and chaos experiments rely on. The constitution requires failure injection through the readiness endpoint, so readiness is controllable from day one.

## ADDED Requirements

### Requirement: Liveness endpoint

The gateway SHALL expose `GET /healthz` returning HTTP 200 while the process is running. Liveness SHALL NOT depend on downstream dependencies.

#### Scenario: Liveness while running

- **WHEN** the gateway process is up
- **THEN** `GET /healthz` returns 200

### Requirement: Readiness endpoint backed by a controllable flag

The gateway SHALL expose `GET /readyz` returning 200 when ready to accept traffic and 503 when not. Readiness SHALL be an internal flag that the process itself flips (during startup and drain), designed so failure-injection experiments can force not-ready deliberately.

#### Scenario: Ready while serving

- **WHEN** the gateway has finished startup and is not draining
- **THEN** `GET /readyz` returns 200

#### Scenario: Not ready while draining

- **WHEN** the gateway is draining
- **THEN** `GET /readyz` returns 503 while in-flight requests continue to be served

### Requirement: Graceful drain on termination signal

On SIGTERM or SIGINT, the gateway SHALL first turn not-ready, then allow in-flight requests to complete within a configurable grace period, then flush telemetry and exit. Requests still in flight when the signal arrives SHALL NOT be cut before the grace period expires.

#### Scenario: In-flight requests complete during drain

- **WHEN** SIGTERM arrives while a request is in flight
- **THEN** readiness turns 503, the in-flight request completes normally, and the process exits within the grace period

#### Scenario: Grace period bounds shutdown

- **WHEN** an in-flight request outlives the configured grace period during drain
- **THEN** the process still exits at the end of the grace period
