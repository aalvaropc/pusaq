# tenant-auth

## MODIFIED Requirements

### Requirement: Dev mode without configured keys is explicit

When `PUSAQ_API_KEYS` is unset, the gateway SHALL serve requests without authentication, attribute every request to the `dev` tenant, and log a warning at startup that authentication is disabled. This mode SHALL never be silent. A `PUSAQ_API_KEYS` that is set but holds no `key:tenant` pair SHALL fail startup. Configuration errors SHALL name positions or tenants, never an API key.

#### Scenario: Dev mode attribution

- **WHEN** no keys are configured and a request arrives without credentials
- **THEN** the request is served and its cost event carries the `dev` tenant

#### Scenario: Empty key list fails startup

- **WHEN** `PUSAQ_API_KEYS` is set to an empty value
- **THEN** the gateway fails to start with a configuration error instead of entering dev mode

#### Scenario: Errors never echo keys

- **WHEN** `PUSAQ_API_KEYS` repeats a key or has a malformed pair
- **THEN** the startup error names the tenants or the pair's position, and does not contain the key
