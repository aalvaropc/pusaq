## MODIFIED Requirements

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


## ADDED Requirements

### Requirement: Pending shadow samples have a byte budget

Queued and in-progress shadow samples SHALL share a positive configurable retained-data budget, defaulting to 16 MiB. Samples that exceed available capacity SHALL be dropped and counted without waiting for a worker. Capacity SHALL be returned on processing completion or failed enqueue. Closing the evaluator concurrently with an offer SHALL not panic or retain unprocessed samples.

#### Scenario: Pending prompt bytes exceed capacity

- **WHEN** an offer exceeds the available byte budget even though a queue slot exists
- **THEN** it is dropped immediately and counted

#### Scenario: Processing and shutdown return capacity

- **WHEN** accepted evaluations complete or the evaluator drains during shutdown
- **THEN** their reserved bytes are returned and the index retains no raw prompt
