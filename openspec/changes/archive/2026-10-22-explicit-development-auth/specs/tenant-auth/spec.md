## MODIFIED Requirements

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
