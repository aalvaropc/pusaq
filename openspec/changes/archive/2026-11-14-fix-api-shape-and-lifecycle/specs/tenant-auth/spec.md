## MODIFIED Requirements

### Requirement: Requests are authenticated by API key and attributed to a tenant

When API keys are configured, the gateway SHALL require `Authorization: Bearer <key>` on API requests, resolve the key to its tenant before any other request processing, and attribute the request (including its cost event) to that tenant. The scheme name SHALL be matched case-insensitively, and one or more spaces or horizontal tabs SHALL separate it from the key; any other scheme SHALL count as no bearer token. Authentication SHALL happen before any provider is called.

#### Scenario: Valid key attributes the tenant

- **WHEN** a request presents a configured API key
- **THEN** the request is processed normally and its cost event carries that key's tenant

#### Scenario: Scheme case and separator

- **WHEN** a request presents a configured key as `bearer <key>`, `BEARER <key>` or `Bearer` followed by a tab and the key
- **THEN** the request is authenticated as that key's tenant

#### Scenario: Missing key rejected

- **WHEN** API keys are configured and a request presents no bearer token
- **THEN** the gateway responds 401 with the normalized error code `unauthorized`, no provider is called, and a cost event with the error outcome is still emitted

#### Scenario: Unknown key rejected

- **WHEN** a request presents a bearer token that matches no configured key
- **THEN** the gateway responds 401 with code `unauthorized` and no provider is called
