## MODIFIED Requirements

### Requirement: Only structurally cacheable requests are evaluated

A request SHALL be eligible for shadow evaluation only when it is a successful completion with exactly one user message, optionally preceded by system messages, whose content carries no sensitive data, no instruction-override phrasing and no time-dependence cue, whose explicit temperature (if any) is at most 0.7, and whose tenant has not opted out. Multi-turn conversations SHALL never be evaluated or inserted; an assistant turn, an assistant tool call or a tool result makes a request multi-turn. A single-turn request that declares tools is eligible under the namespace of its tools. Eligibility SHALL be decided once per request, and when a request is ineligible the first applicable reason — `tenant_off`, `multi_turn`, `sensitive`, `injection`, `temporal`, `temperature` — SHALL be recorded on its cost event. Both buffered and streamed completions are eligible.

#### Scenario: Multi-turn excluded by construction

- **WHEN** a completion has more than one user message
- **THEN** no shadow evaluation happens for it and the event records `multi_turn`

#### Scenario: Content keeps a prompt out of the cache

- **WHEN** a single-turn completion's prompt contains a card number, or asks about today's exchange rate
- **THEN** no shadow evaluation happens for it and the event records `sensitive` or `temporal`

#### Scenario: High temperature is not repeatable

- **WHEN** a completion sets `temperature` above 0.7
- **THEN** no shadow evaluation happens for it and the event records `temperature`; at 0.7 or below it is evaluated

#### Scenario: Tool turns are multi-turn

- **WHEN** a completion carries an assistant tool call and a tool result
- **THEN** no shadow evaluation happens for it and the event records `multi_turn`

#### Scenario: Single-turn request with tools is evaluated

- **WHEN** a single-turn completion that declares tools is answered with a tool call, buffered or streamed
- **THEN** it is evaluated under a namespace that covers its tools, with a fingerprint that covers the tool call

### Requirement: Namespaces isolate incompatible answer spaces

Cache entries and lookups SHALL be scoped to a namespace derived from at least: the tenant; the route's physical model; the model the provider reported in the evaluated response, or the route's physical model when the provider reported none; the system-prompt content; the request's tools — a hash of the canonical tool definitions in declared order with object keys sorted, of `tool_choice` and of `parallel_tool_calls`, each absence keyed as such, or a fixed marker when the request declares no tools; the request's explicit temperature and its explicit `max_tokens` (the absence of either is its own key); and the version of the eligibility rules and namespace derivation — plus a fixed marker for the response format, which the accepted request subset does not carry. The components SHALL be encoded so that no two distinct combinations derive the same namespace. Entries in one namespace SHALL never match lookups in another. Language is not a namespace component: a prompt and its translation share a namespace when every other component matches.

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

#### Scenario: Different tools never match

- **WHEN** the same prompt arrives without tools, with one tool, with that tool under another description or parameter schema, with a different `tool_choice`, or with a different `parallel_tool_calls`
- **THEN** the evaluations use distinct namespaces and do not match each other's entries

#### Scenario: Key order does not split a namespace

- **WHEN** two requests declare the same tools with the keys of their objects in a different order
- **THEN** the evaluations use the same namespace

### Requirement: Shadow records make τ calibration possible offline

Every shadow evaluation with at least one candidate neighbor SHALL record: the request ID, the namespace, the best similarity found, the neighbor's entry ID, the neighbor's response fingerprint, the request's own response fingerprint, the embedder latency, and whether the similarity clears the configured threshold τ. Records SHALL be emitted regardless of whether τ is cleared, so precision/recall for any τ is computable from the record stream alone. A response fingerprint SHALL cover the answer's text and each of its tool calls' function name and arguments, without call IDs, and SHALL be the same for a buffered answer and for the same answer streamed in any fragmentation. A stream whose tool-call indices fall outside 0–127 SHALL not be evaluated.

#### Scenario: Near-duplicate recorded as a would-be hit

- **WHEN** an eligible request's best similarity clears τ
- **THEN** its shadow record marks a hit and carries both response fingerprints for false-positive analysis

#### Scenario: Below-τ similarity still recorded

- **WHEN** an eligible request's best similarity is below τ
- **THEN** the record still carries that similarity, enabling offline sweeps of alternative thresholds

#### Scenario: Tool calls with other arguments fingerprint differently

- **WHEN** two answers call the same function with different arguments, or the same call under different call IDs
- **THEN** the first pair carries different fingerprints and the second pair the same one, whether each answer was buffered or streamed
