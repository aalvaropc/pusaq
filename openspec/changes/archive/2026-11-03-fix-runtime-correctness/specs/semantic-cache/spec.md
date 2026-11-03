## MODIFIED Requirements

### Requirement: τ is chosen by reproducible calibration with a precision floor

The similarity threshold τ SHALL be chosen by a calibration procedure over a labeled dataset of equivalent and trap pairs, selecting the smallest τ whose one-sided exact (Clopper–Pearson) lower confidence bound on precision meets a configured floor (default 0.99) at a configured confidence (default 0.95) — never by hit-rate, and never from the point estimate alone. The procedure SHALL run as a single command using the same embedder implementations the gateway uses, SHALL emit the full precision/recall curve with each point's predicted hits, false hits and precision lower bound, and SHALL fail explicitly when no τ reaches the floor, stating whether no τ reaches it even by point estimate or the best one lacks support, and how many predicted hits without a false hit certification needs. A τ with no predicted hit has undefined precision; it SHALL be reported as undefined, never as zero, and SHALL never qualify. Runs over the same dataset and embedder SHALL be reproducible.

#### Scenario: τ\* selected at the precision floor

- **WHEN** the calibration runs over a labeled dataset where some τ's precision lower bound reaches the floor
- **THEN** it reports the smallest such τ with its precision, lower bound, predicted hits and recall, and writes the full curve

#### Scenario: No τ reaches the floor

- **WHEN** no threshold in the sweep meets the precision floor
- **THEN** the calibration fails explicitly instead of recommending a τ

#### Scenario: Insufficient support does not certify

- **WHEN** a τ's only predicted hit is a true hit, so its observed precision is 1.0
- **THEN** that τ does not qualify, and the failure names the number of predicted hits certification needs

#### Scenario: Undefined precision is reported as undefined

- **WHEN** a τ in the sweep predicts no hit
- **THEN** its precision appears as `undefined` in the curve, not as 0

#### Scenario: Reproducible runs

- **WHEN** the calibration runs twice over the same dataset with the same embedder
- **THEN** the reported τ\* and curve are identical
