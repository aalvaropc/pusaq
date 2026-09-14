# chat-completions

## MODIFIED Requirements

### Requirement: Models are addressed by alias

The gateway SHALL accept only configured model aliases (`pusaq/fast`, `pusaq/best`, `pusaq/auto`) in the `model` field. `pusaq/fast` and `pusaq/best` SHALL each resolve to a provider and upstream model from configuration; the physical model each resolves to SHALL be configurable per alias. `pusaq/auto` SHALL have no physical model of its own and SHALL resolve to one of the concrete aliases through the routing rules. Configuration naming an unknown alias, or naming `pusaq/auto` as a route, SHALL be rejected at startup. Physical upstream model names SHALL live in configuration only.

#### Scenario: Alias resolved from configuration

- **WHEN** a request names a concrete alias
- **THEN** the request is forwarded to the provider and upstream model that configuration maps that alias to

#### Scenario: Per-alias model override

- **WHEN** configuration maps a concrete alias to a specific physical model
- **THEN** requests for that alias are served by that model and report it in `X-Pusaq-Model`

#### Scenario: Unknown alias in configuration rejected at startup

- **WHEN** the route configuration names an alias the gateway does not define
- **THEN** the gateway fails to start with a configuration error

#### Scenario: Auto alias in route configuration rejected at startup

- **WHEN** the route configuration assigns a physical model to `pusaq/auto`
- **THEN** the gateway fails to start with a configuration error explaining that the alias resolves through routing rules

#### Scenario: Unknown model rejected

- **WHEN** a request names a model that is not a configured alias
- **THEN** the gateway responds 400 with code `invalid_request` naming the rejected model, without calling any provider

## ADDED Requirements

### Requirement: pusaq/auto resolves through feature-tag routing rules

The gateway SHALL resolve `pusaq/auto` to a concrete alias using an explicit table from configuration keyed by the request's `metadata.feature_tag`: the rule for the tag when one exists, the default alias otherwise. The table SHALL have a default entry and every target SHALL be a concrete configured alias; violations SHALL be rejected at startup. Every completion response — success, streamed, or error after resolution — SHALL carry `X-Pusaq-Route-Reason` with one of `direct` (a concrete alias was requested), `rule:<tag>`, `default:untagged`, or `default:unknown_tag`. A `metadata.feature_tag` that is not a string SHALL be rejected as `invalid_request` without calling any provider.

#### Scenario: Tagged request follows its rule

- **WHEN** a request for `pusaq/auto` carries a feature tag that a rule names
- **THEN** it is served by that rule's alias and the response reports `rule:<tag>`

#### Scenario: Untagged request uses the default

- **WHEN** a request for `pusaq/auto` carries no feature tag
- **THEN** it is served by the default alias and the response reports `default:untagged`

#### Scenario: Unknown tag uses the default

- **WHEN** a request for `pusaq/auto` carries a feature tag no rule names
- **THEN** it is served by the default alias and the response reports `default:unknown_tag`

#### Scenario: Route reason reported on every response

- **WHEN** a completion is served, streamed, or fails upstream after its alias was resolved
- **THEN** the response carries `X-Pusaq-Route-Reason`, and a concrete alias reports `direct`

#### Scenario: Routing rules are validated at startup

- **WHEN** the routing rules lack a default entry, target `pusaq/auto` or an unknown alias, or repeat a tag
- **THEN** the gateway fails to start with a configuration error

#### Scenario: Non-string feature tag rejected

- **WHEN** a request's `metadata.feature_tag` is not a string
- **THEN** the gateway responds 400 with code `invalid_request` and calls no provider
