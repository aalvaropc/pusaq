## MODIFIED Requirements

### Requirement: Namespaces isolate incompatible answer spaces

Cache entries and lookups SHALL be scoped to a namespace derived from at least: the tenant; the route's physical model; the model the provider reported in the evaluated response, or the route's physical model when the provider reported none; the system-prompt content; the request's explicit temperature and its explicit `max_tokens` (the absence of either is its own key); and the version of the eligibility rules and namespace derivation — plus fixed markers for tools and response format, which the accepted request subset does not carry. The components SHALL be encoded so that no two distinct combinations derive the same namespace. Entries in one namespace SHALL never match lookups in another. Language is not a namespace component: a prompt and its translation share a namespace when every other component matches.

#### Scenario: Different tenants never match

- **WHEN** two tenants send an identical prompt
- **THEN** each is evaluated only against its own tenant's entries

#### Scenario: Different system prompts never match

- **WHEN** the same prompt arrives under different system messages
- **THEN** the evaluations use distinct namespaces and do not match each other's entries

#### Scenario: Different temperatures never match

- **WHEN** the same prompt arrives with no temperature, with `temperature` 0, and with `temperature` 0.5
- **THEN** the three evaluations use distinct namespaces

#### Scenario: Different max_tokens never match

- **WHEN** the same prompt arrives with no `max_tokens`, with `max_tokens` 32, and with `max_tokens` 64
- **THEN** the three evaluations use distinct namespaces and do not match each other's entries

#### Scenario: Different reported models never match

- **WHEN** the same prompt on the same route is answered under two different provider-reported model names, such as two versions behind one upstream alias
- **THEN** the evaluations use distinct namespaces and do not match each other's entries, for buffered and streamed completions alike

#### Scenario: Unreported model keys on the route's model

- **WHEN** the provider reports no model for an evaluated response
- **THEN** the evaluation uses the namespace of a response that reported the route's physical model

#### Scenario: Different routes never match

- **WHEN** two routes with different physical models receive answers that report the same model name
- **THEN** the evaluations use distinct namespaces

#### Scenario: Components cannot shift into each other

- **WHEN** two evaluations differ only in where a byte sequence falls between the reported model and the system prompt
- **THEN** they use distinct namespaces
