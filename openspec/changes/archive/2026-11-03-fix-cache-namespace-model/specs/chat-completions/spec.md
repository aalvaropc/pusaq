## MODIFIED Requirements

### Requirement: Served model is reported

Every chat-completions response — success or error after model resolution — SHALL carry the `X-Pusaq-Model` header with the route's physical model: the upstream model that configuration maps the resolved alias to, which every backend in the fallback chain is called with. The header SHALL NOT be taken from the provider's answer. The `model` field of a buffered response body SHALL be the model name the provider reported, and streamed chunks SHALL keep the provider's own `model` values; either can differ from the header, for example a dated version behind an upstream alias.

#### Scenario: Header on success

- **WHEN** a completion succeeds
- **THEN** the response includes `X-Pusaq-Model` with the route's physical model

#### Scenario: Body reports the provider's model

- **WHEN** the provider answers a buffered completion reporting a model name other than the route's physical model
- **THEN** `X-Pusaq-Model` carries the route's physical model and the body's `model` carries the name the provider reported
