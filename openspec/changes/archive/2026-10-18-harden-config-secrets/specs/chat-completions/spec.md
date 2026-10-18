# chat-completions

## MODIFIED Requirements

### Requirement: Upstream calls are authenticated when configured

An upstream backend MAY carry an API key in configuration. When present, the gateway SHALL authenticate every call to that backend with it; when absent, no credentials SHALL be sent. Upstream keys SHALL never appear in logs or cost events, and configuration errors SHALL never echo an upstream URL. Two upstreams whose names map to the same key variable SHALL fail startup.

#### Scenario: Keyed upstream receives credentials

- **WHEN** an upstream is configured with an API key and a request is routed to it
- **THEN** the upstream call carries that key as a bearer credential

#### Scenario: Keyless upstream receives none

- **WHEN** an upstream has no configured key
- **THEN** its calls carry no Authorization credential

#### Scenario: Colliding key variables fail startup

- **WHEN** two upstreams are named `my-api` and `my_api`
- **THEN** the gateway fails to start, naming both upstreams and the shared key variable
