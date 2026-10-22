# tenant-auth Specification

## Purpose
API-key authentication and tenant attribution. The tenant is the unit everything else is governed by — limits, budgets, policies, cost reports — so every request the pipeline admits must be attributed to exactly one tenant before any downstream work happens.

## Requirements

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

The gateway SHALL fail startup without API keys unless `PUSAQ_DEV_MODE=true` explicitly enables unauthenticated development. Development mode SHALL default to false, SHALL reject configured API keys, SHALL attribute requests to the `dev` tenant, and SHALL log a startup warning. A `PUSAQ_API_KEYS` that is set but holds no `key:tenant` pair SHALL fail startup. Configuration errors SHALL name positions or tenants, never an API key.

#### Scenario: Dev mode attribution

- **WHEN** development mode is explicitly enabled without API keys and a request arrives without credentials
- **THEN** the request is served and its cost event carries the `dev` tenant

#### Scenario: Empty key list fails startup

- **WHEN** `PUSAQ_API_KEYS` is set to an empty value
- **THEN** the gateway fails to start with a configuration error instead of entering dev mode

#### Scenario: Errors never echo keys

- **WHEN** `PUSAQ_API_KEYS` repeats a key or has a malformed pair
- **THEN** the startup error names the tenants or the pair's position, and does not contain the key

#### Scenario: Missing authentication configuration fails startup

- **WHEN** API keys are absent and development mode is not explicitly enabled
- **THEN** startup fails without serving requests

#### Scenario: Conflicting authentication configuration fails startup

- **WHEN** development mode and API keys are both configured
- **THEN** startup fails without echoing credentials

### Requirement: Operational endpoints stay unauthenticated

`GET /healthz` and `GET /readyz` SHALL respond without credentials regardless of key configuration — load balancers and probes do not authenticate.

#### Scenario: Probes without credentials

- **WHEN** API keys are configured
- **THEN** `/healthz` and `/readyz` still respond without an Authorization header
