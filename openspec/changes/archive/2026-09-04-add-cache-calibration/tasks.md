# Tasks: add-cache-calibration

## 1. Sweep library

- [x] 1.1 `internal/calibrate`: pair scoring (one embed per side, one similarity per pair) + τ sweep (precision/recall/F1 per step) + τ\* selection with the precision floor and explicit no-qualifying-τ failure; tests with a fake embedder on synthetic geometry where precision/recall are hand-computable, incl. the undefined-precision edge and reproducibility.

## 2. CLI and dataset

- [x] 2.1 `cmd/cachecal`: flags (`-pairs`, `-out`, `-floor`, `-embedder local|openai`, `-base-url`, `-embed-model`, key via env `CACHECAL_API_KEY`); reads labeled CSV (rejecting malformed rows/labels), writes the curve CSV, prints τ\* + recall, exit 1 when no τ qualifies; CSV parse tests.
- [x] 2.2 `testdata/calibration/seed-pairs.csv`: ~30 equivalent + ~30 trap pairs (EN/ES; entity swap, negation, quantity, date traps); documented as harness-proof, not production τ.

## 3. Delivery

- [x] 3.1 Makefile `calibrate` target; README calibration section (precision-floor framing, seed-vs-production dataset honesty, which embedder produced which number).
- [x] 3.2 Full suite `-race` green; lint + vet clean; `make calibrate` runs end-to-end on the seed set with the local embedder and is reproducible (identical output on re-run).
- [x] 3.3 Conventional commit(s) on `feat/cache-calibration`, push, PR stacked on `feat/semantic-cache` (auto-retarget on merge); CI green.
