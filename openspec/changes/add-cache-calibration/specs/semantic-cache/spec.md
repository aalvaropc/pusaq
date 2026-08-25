# semantic-cache

## ADDED Requirements

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
