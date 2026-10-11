# semantic-cache Specification

## Purpose

Shadow-mode measurement of a semantic cache: what would it hit, how similar are the neighbors, what would the embedding toll cost, and which hits would have been wrong — measured against real traffic without ever serving a cached answer. Serving from the cache is a separate capability, gated on these numbers.

## Requirements

### Requirement: Shadow mode never serves and is off by default

The semantic cache SHALL run in one of two modes: `off` (default) or `shadow`. In shadow mode the gateway SHALL NOT serve any response from the cache; every request is answered by its provider as if the cache did not exist. Enabling shadow mode SHALL be announced at startup.

#### Scenario: Shadow mode is observational only

- **WHEN** shadow mode is enabled and a request closely matches a cached entry
- **THEN** the response still comes from the provider, and the match is only recorded

#### Scenario: Off means silent

- **WHEN** cache mode is `off`
- **THEN** no shadow evaluation happens and no cache events are emitted

### Requirement: Evaluation never perturbs the request path

Shadow evaluation (embedding, neighbor search, recording, insertion) SHALL run asynchronously after the response, under a bounded concurrency budget. When the budget is saturated, evaluations SHALL be dropped and counted rather than queued unboundedly or blocking any request. An embedder failure or timeout SHALL skip that evaluation, counted, without affecting the request.

#### Scenario: Slow embedder does not delay responses

- **WHEN** the embedder is slow or unavailable
- **THEN** request latency is unaffected and the affected evaluations are skipped and counted

#### Scenario: Saturation drops and counts

- **WHEN** evaluations are produced faster than the concurrency budget drains them
- **THEN** excess evaluations are dropped and the drop count is observable

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

Cache entries and lookups SHALL be scoped to a namespace derived from at least: the tenant, the served physical model, the system-prompt content, the request's explicit temperature (or the absence of one, as its own key), and the version of the eligibility rules — extended mechanically as the API subset grows (tools, response format). Entries in one namespace SHALL never match lookups in another.

#### Scenario: Different tenants never match

- **WHEN** two tenants send an identical prompt
- **THEN** each is evaluated only against its own tenant's entries

#### Scenario: Different system prompts never match

- **WHEN** the same prompt arrives under different system messages
- **THEN** the evaluations use distinct namespaces and do not match each other's entries

#### Scenario: Different temperatures never match

- **WHEN** the same prompt arrives with no temperature, with `temperature` 0, and with `temperature` 0.5
- **THEN** the three evaluations use distinct namespaces

### Requirement: Shadow records make τ calibration possible offline

Every shadow evaluation with at least one candidate neighbor SHALL record: the request ID, the namespace, the best similarity found, the neighbor's entry ID, the neighbor's response fingerprint, the request's own response fingerprint, the embedder latency, and whether the similarity clears the configured threshold τ. Records SHALL be emitted regardless of whether τ is cleared, so precision/recall for any τ is computable from the record stream alone.

#### Scenario: Near-duplicate recorded as a would-be hit

- **WHEN** an eligible request's best similarity clears τ
- **THEN** its shadow record marks a hit and carries both response fingerprints for false-positive analysis

#### Scenario: Below-τ similarity still recorded

- **WHEN** an eligible request's best similarity is below τ
- **THEN** the record still carries that similarity, enabling offline sweeps of alternative thresholds

### Requirement: The index is bounded

The shadow index SHALL cap its entry count and evict least-recently-used entries at the cap. The cap SHALL be configurable.

#### Scenario: Cap enforced

- **WHEN** the index is at capacity and a new entry is inserted
- **THEN** the least-recently-used entry is evicted and the count stays at the cap

### Requirement: τ is chosen by reproducible calibration with a precision floor

The similarity threshold τ SHALL be chosen by a calibration procedure over a labeled dataset of equivalent and trap pairs, selecting the smallest τ whose precision meets a configured floor (default 0.99) — never by hit-rate. The procedure SHALL run as a single command using the same embedder implementations the gateway uses, SHALL emit the full precision/recall curve, and SHALL fail explicitly when no τ reaches the floor. Runs over the same dataset and embedder SHALL be reproducible.

#### Scenario: τ\* selected at the precision floor

- **WHEN** the calibration runs over a labeled dataset where some τ reaches the precision floor
- **THEN** it reports the smallest such τ with its precision and recall, and writes the full curve

#### Scenario: No τ reaches the floor

- **WHEN** no threshold in the sweep meets the precision floor
- **THEN** the calibration fails explicitly instead of recommending a τ

#### Scenario: Reproducible runs

- **WHEN** the calibration runs twice over the same dataset with the same embedder
- **THEN** the reported τ\* and curve are identical
