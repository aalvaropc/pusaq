# semantic-cache Specification

## Purpose

Shadow-mode measurement of a semantic cache: what would it hit, how similar are the neighbors, what would the embedding toll cost, and which hits would have been wrong — measured against real traffic without ever serving a cached answer. Nothing is ever served from the cache.

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

### Requirement: Shadow records make τ calibration possible offline

Every shadow evaluation with at least one candidate neighbor SHALL record: the request ID, the namespace, the best similarity found, the neighbor's entry ID, the neighbor's response fingerprint, the request's own response fingerprint, the embedder latency, and whether the similarity clears the configured threshold τ. Records SHALL be emitted regardless of whether τ is cleared, so precision/recall for any τ is computable from the record stream alone.

#### Scenario: Near-duplicate recorded as a would-be hit

- **WHEN** an eligible request's best similarity clears τ
- **THEN** its shadow record marks a hit and carries both response fingerprints for false-positive analysis

#### Scenario: Below-τ similarity still recorded

- **WHEN** an eligible request's best similarity is below τ
- **THEN** the record still carries that similarity, enabling offline sweeps of alternative thresholds

### Requirement: The index is bounded

The shadow index SHALL cap both entry count and retained data bytes, evicting least-recently-used entries to satisfy both caps. It SHALL retain vectors and fingerprints without raw prompts and SHALL release references on eviction. The default retained-data budget SHALL be 64 MiB, configurable as a positive byte count. An individual entry exceeding that budget SHALL be skipped and counted.

#### Scenario: Cap enforced

- **WHEN** the index is at capacity and a new entry is inserted
- **THEN** the least-recently-used entry is evicted and the count stays at the cap

#### Scenario: Byte cap enforced

- **WHEN** an entry fits the byte budget but requires eviction
- **THEN** enough least-recently-used entries are removed to keep retained data within the configured cap

#### Scenario: Oversized entry is skipped

- **WHEN** one entry exceeds the entire byte budget
- **THEN** it is not inserted and a skip is counted without changing the served response

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

### Requirement: The embedder is configured on its own

The shadow cache SHALL embed with the local embedder by default, or with an OpenAI-compatible endpoint given by its own base URL, model, and optional bearer key, which it calls at the base URL followed by `/embeddings`. The embedder SHALL NOT depend on the chat upstreams. Its endpoint SHALL follow the same URL and credential-transport restrictions as provider endpoints, and its client SHALL reject redirects without resending its payload or credentials. Its key SHALL never appear in logs or cost events, configuration errors SHALL never echo its URL, and a key without an endpoint embedder SHALL fail startup.

#### Scenario: Endpoint embedder

- **WHEN** the embedder is configured as an OpenAI-compatible endpoint with a model and a key
- **THEN** embeddings are requested at the base URL followed by `/embeddings` with that model and key, whatever the chat upstreams are

#### Scenario: Invalid embedder configuration fails startup

- **WHEN** the embedder endpoint is not an absolute URL, has no model, or a key is set for the local embedder
- **THEN** the gateway fails to start, and the error does not contain the URL

### Requirement: Pending shadow samples have a byte budget

Queued and in-progress shadow samples SHALL share a positive configurable retained-data budget, defaulting to 16 MiB. Samples that exceed available capacity SHALL be dropped and counted without waiting for a worker. Capacity SHALL be returned on processing completion or failed enqueue. Closing the evaluator concurrently with an offer SHALL not panic or retain unprocessed samples.

#### Scenario: Pending prompt bytes exceed capacity

- **WHEN** an offer exceeds the available byte budget even though a queue slot exists
- **THEN** it is dropped immediately and counted

#### Scenario: Processing and shutdown return capacity

- **WHEN** accepted evaluations complete or the evaluator drains during shutdown
- **THEN** their reserved bytes are returned and the index retains no raw prompt
