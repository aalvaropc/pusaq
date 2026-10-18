# Design: add-cache-calibration

## Context

Shadow records carry best-similarity per request, but choosing τ needs ground truth: labeled pairs where "equivalent" and "trap" are known. The embedder seam (`internal/embed`) exists with local and HTTP implementations. See proposal.md.

## Goals / Non-Goals

**Goals:**

- τ\* as the output of a command, not a judgment call; the curve as an artifact.
- Calibration and production sharing one embedding code path — a τ calibrated with a different embedder than production runs is meaningless, so the harness makes that mistake hard.

**Non-Goals:**

- A production dataset (≥200+200 pairs from a real corpus); plotting (the CSV is the interface; any tool plots it); calibrating the local embedder as if its numbers meant semantic quality (the README says which embedder a number came from) — out of scope for this change.

## Decisions

1. **Sweep logic lives in `internal/calibrate`, CLI in `cmd/cachecal`.** The library takes labeled pairs + an `embed.Embedder` and returns the curve + τ\* — unit-testable with synthetic vectors and a fake embedder, no CLI involved. The CLI does flags, CSV I/O, and exit codes.
2. **Per-pair similarity, computed once.** Each pair is embedded and scored once; the τ sweep is then pure arithmetic over the scored pairs (predicted-equivalent iff sim ≥ τ): precision = TP/(TP+FP) over trap/equivalent labels, recall = TP/(TP+FE). Embedding N pairs once instead of once-per-τ keeps API-embedder runs affordable.
3. **Precision floor semantics**: τ\* = min{τ : precision(τ) ≥ floor} — the *smallest* qualifying τ maximizes recall at the required precision. Undefined precision (no predicted positives) counts as passing the floor with recall 0 — reported but ranked after any τ with actual recall; if nothing with recall > 0 passes, the run fails with exit 1 and says so.
4. **In-repo Go, no Python.** The design doc originally suggested Python for analysis; sharing the exact embedder implementation outweighs pandas — fidelity of the number beats convenience of the notebook. The curve CSV is consumable anywhere.
5. **Seed dataset is honest about its size**: ~30 equivalent + ~30 trap pairs (EN/ES; entity swaps, negations, quantities, dates). It proves the harness and documents the *shape* of a good dataset; the README states the production target explicitly.

## Risks / Trade-offs

- **A τ\* from the seed set is not a production τ** — small n, hand-written pairs. Stated everywhere the number appears.
- **The local embedder will score traps high** (lexical overlap: "capital of Peru" vs "capital of Bolivia" differ by one word) — which is exactly the *point*: the harness makes the local embedder's unfitness for semantic policy measurable instead of assumed, and the same command runs against a real embedder.
