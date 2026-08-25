# tenant-auth

## Purpose

API-key authentication and tenant attribution. The tenant is the unit everything else is governed by — limits, budgets, policies, cost reports — so every request the pipeline admits must be attributed to exactly one tenant before any downstream work happens.

## ADDED Requirements

### Requirement: Requests are authenticated by API key and attributed to a tenant

When API keys are configured, the gateway SHALL require `Authorization: Bearer <key>` on API requests, resolve the key to its tenant before any other request processing, and attribute the request (including its cost event) to that tenant. Authentication SHALL happen before any provider is called.

#### Scenario: Valid key attributes the tenant

- **WHEN** a request presents a configured API key
- **THEN** the request is processed normally and its cost event carries that key's tenant

#### Scenario: Missing key rejected

- **WHEN** API keys are configured and a request presents no bearer token
- **THEN** the gateway responds 401 with the normalized error code `unauthorized`, no provider is called, and a cost event with the error outcome is still emitted

#### Scenario: Unknown key rejected

- **WHEN** a request presents a bearer token that matches no configured key
- **THEN** the gateway responds 401 with code `unauthorized` and no provider is called

### Requirement: Dev mode without configured keys is explicit

When no API keys are configured, the gateway SHALL serve requests without authentication, attribute every request to the `dev` tenant, and log a warning at startup that authentication is disabled. This mode SHALL never be silent.

#### Scenario: Dev mode attribution

- **WHEN** no keys are configured and a request arrives without credentials
- **THEN** the request is served and its cost event carries the `dev` tenant

### Requirement: Operational endpoints stay unauthenticated

`GET /healthz` and `GET /readyz` SHALL respond without credentials regardless of key configuration — load balancers and probes do not authenticate.

#### Scenario: Probes without credentials

- **WHEN** API keys are configured
- **THEN** `/healthz` and `/readyz` still respond without an Authorization header
