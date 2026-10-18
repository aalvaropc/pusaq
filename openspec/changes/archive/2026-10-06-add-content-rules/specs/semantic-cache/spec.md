# semantic-cache

## MODIFIED Requirements

### Requirement: Only structurally cacheable requests are evaluated

A request SHALL be eligible for shadow evaluation only when it is a successful completion with exactly one user message, optionally preceded by system messages, whose content carries no sensitive data, no instruction-override phrasing and no time-dependence cue, whose explicit temperature (if any) is at most 0.7, and whose tenant has not opted out. Multi-turn conversations SHALL never be evaluated or inserted. Eligibility SHALL be decided once per request, and when a request is ineligible the first applicable reason — `tenant_off`, `multi_turn`, `sensitive`, `injection`, `temporal`, `temperature` — SHALL be recorded on its cost event. Both buffered and streamed completions are eligible.

#### Scenario: Multi-turn excluded by construction

- **WHEN** a completion has more than one user message
- **THEN** no shadow evaluation happens for it and the event records `multi_turn`

#### Scenario: Content keeps a prompt out of the cache

- **WHEN** a single-turn completion's prompt contains a card number, or asks about today's exchange rate
- **THEN** no shadow evaluation happens for it and the event records `sensitive` or `temporal`

#### Scenario: High temperature is not repeatable

- **WHEN** a completion sets `temperature` above 0.7
- **THEN** no shadow evaluation happens for it and the event records `temperature`; at 0.7 or below it is evaluated

### Requirement: Namespaces isolate incompatible answer spaces

Cache entries and lookups SHALL be scoped to a namespace derived from at least: the tenant, the served physical model, the system-prompt content, the request's explicit temperature (or the absence of one, as its own key), and the version of the eligibility rules — plus fixed markers for tools and response format, which the accepted request subset does not carry. Entries in one namespace SHALL never match lookups in another.

#### Scenario: Different tenants never match

- **WHEN** two tenants send an identical prompt
- **THEN** each is evaluated only against its own tenant's entries

#### Scenario: Different system prompts never match

- **WHEN** the same prompt arrives under different system messages
- **THEN** the evaluations use distinct namespaces and do not match each other's entries

#### Scenario: Different temperatures never match

- **WHEN** the same prompt arrives with no temperature, with `temperature` 0, and with `temperature` 0.5
- **THEN** the three evaluations use distinct namespaces
