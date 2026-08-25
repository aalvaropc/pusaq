# chat-completions

## MODIFIED Requirements

### Requirement: Models are addressed by alias

The gateway SHALL accept only configured model aliases (initially `pusaq/fast`, `pusaq/best`, `pusaq/auto`) in the `model` field and SHALL resolve each alias to a provider and upstream model from configuration. The physical model each alias resolves to SHALL be configurable per alias; configuration naming an unknown alias SHALL be rejected at startup. Physical upstream model names SHALL live in configuration only.

#### Scenario: Alias resolved from configuration

- **WHEN** a request names a configured alias
- **THEN** the request is forwarded to the provider and upstream model that configuration maps that alias to

#### Scenario: Per-alias model override

- **WHEN** configuration maps an alias to a specific physical model
- **THEN** requests for that alias are served by that model and report it in `X-Pusaq-Model`

#### Scenario: Unknown alias in configuration rejected at startup

- **WHEN** the route configuration names an alias the gateway does not define
- **THEN** the gateway fails to start with a configuration error

#### Scenario: Unknown model rejected

- **WHEN** a request names a model that is not a configured alias
- **THEN** the gateway responds 400 with code `invalid_request` naming the rejected model, without calling any provider

## ADDED Requirements

### Requirement: Upstream calls are authenticated when configured

An upstream backend MAY carry an API key in configuration. When present, the gateway SHALL authenticate every call to that backend with it; when absent, no credentials SHALL be sent. Upstream keys SHALL never appear in logs or cost events.

#### Scenario: Keyed upstream receives credentials

- **WHEN** an upstream is configured with an API key and a request is routed to it
- **THEN** the upstream call carries that key as a bearer credential

#### Scenario: Keyless upstream receives none

- **WHEN** an upstream has no configured key
- **THEN** its calls carry no Authorization credential
