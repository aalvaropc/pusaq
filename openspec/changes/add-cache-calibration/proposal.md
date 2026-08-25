# Proposal: add-cache-calibration

## Why

The shadow cache records similarities; someone still has to decide τ. The founding doc's rule is non-negotiable: τ is chosen for a precision floor (≥ 0.99 — a false positive is a wrong answer served to a user), never for hit-rate, and the curve must be reproducible. This change turns that rule into a command: a calibration harness that sweeps τ over a labeled pair dataset using the *same* embedder implementations the gateway runs, and prints τ\* with its precision/recall curve.

## What Changes

- `cmd/cachecal`: a CLI that reads a labeled CSV (`text_a,text_b,label` with label ∈ `equivalent`|`trap`), embeds both sides with `internal/embed` (flags select `local` or an OpenAI-compatible endpoint + model + key env), sweeps τ over [0.80, 0.995] in 0.005 steps, writes the `(τ, precision, recall, f1)` curve as CSV, and prints `τ* = min{τ : precision(τ) ≥ floor}` (floor flag, default 0.99) with its recall — or fails loudly when no τ reaches the floor.
- A seed labeled dataset (`testdata/calibration/seed-pairs.csv`): equivalent paraphrase pairs and trap pairs (entity swaps, negations, date changes; EN and ES) in the capital-of-Peru/Bolivia mold. Honest scope: the founding doc's ≥200+200-pair target belongs to the experiments phase with a real traffic corpus — the seed proves the harness, not the final τ.
- `make calibrate` target; README calibration section.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `semantic-cache`: τ SHALL be chosen by a reproducible calibration procedure with a precision floor, runnable as a single command over a labeled dataset.

## Impact

- New code: `cmd/cachecal`, `internal/calibrate` (sweep logic, testable without the CLI), `testdata/calibration/seed-pairs.csv`.
- Modified: Makefile, README.
- No new dependencies; the embedder seam is reused unchanged — calibration numbers and shadow numbers come from identical code paths.
