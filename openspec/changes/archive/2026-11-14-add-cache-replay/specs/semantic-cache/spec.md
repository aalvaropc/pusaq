## ADDED Requirements

### Requirement: Labeled traffic replay pairs the hit-rate with its false-positive rate

The repository SHALL provide a replay command that samples a deterministic request sequence from a labeled corpus — answers ranked by a seeded Zipf distribution with a configurable exponent, paraphrases chosen uniformly — and drives every request through the shadow evaluation path the gateway uses: the namespace derivation, the content-rule eligibility, the embedder, the nearest-neighbor search, and insertion after evaluation. A request SHALL be a predicted hit at τ when its nearest neighbor's similarity reaches τ, and a predicted hit SHALL be false when the neighbor carries a different labeled answer. For the configured τ and every τ of a sweep, the replay SHALL report the hit-rate over eligible requests only together with the false-positive rate — false hits over predicted hits — and the counts behind both. It SHALL report net savings as the provider cost avoided by correct hits minus the embedding toll of every eligible request, the provider cost skipped by false hits separately and never as savings, latency p50 and p95 with and without the cache, and the break-even hit-rate. Runs with the same corpus, seed and parameters SHALL produce the same request sequence, hit and false-hit counts, and costs.

#### Scenario: Hit-rate is paired with its false-positive rate

- **WHEN** a replay predicts hits, some of whose neighbors carry a different labeled answer
- **THEN** the output shows, at the configured τ and at each τ of the sweep, the hit-rate over eligible requests next to the false-positive rate, with the eligible requests, predicted hits and false hits counted

#### Scenario: The first request of a namespace is never a hit

- **WHEN** a request is the first eligible one in its namespace
- **THEN** it finds no neighbor and counts as a miss, because insertion follows evaluation

#### Scenario: Ineligible requests bypass the cache

- **WHEN** a replayed prompt carries a time-dependence cue
- **THEN** it is not embedded, pays no embedding toll, takes its provider latency with and without the cache, and is counted under its content-rule reason

#### Scenario: Wrong hits are not savings

- **WHEN** some predicted hits are false
- **THEN** net savings count only the provider cost avoided by correct hits minus the embedding toll of every eligible request, and the provider cost the false hits skip is reported apart

#### Scenario: Cross-language neighbors are false hits

- **WHEN** a request's nearest neighbor is the same intent in another language and reaches τ
- **THEN** the hit counts as false, since the cached answer is in the other language

#### Scenario: Latency with and without the cache

- **WHEN** a replay runs with a provider latency distribution
- **THEN** latency without the cache is each request's provider latency, and with the cache a hit takes its measured embed and search time and a miss adds its provider latency to that, both reported as nearest-rank p50 and p95

#### Scenario: Reproducible replay

- **WHEN** the replay runs twice with the same corpus, seed and parameters
- **THEN** the request sequence, hit and false-hit counts and costs are identical, while measured embed and search times may differ
