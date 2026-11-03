## 1. Report

- [x] 1.1 Parse gateway log lines (prefix, malformed, unknown and duplicate records) and select by run id with request-id joins; verify with fixture lines and contract tests against gateway, telemetry and semcache output.
- [x] 1.2 Aggregate the six views (cost, latency, outcomes, fallback, limits, shadow cache); verify per-tenant sums, nearest-rank percentiles, limit kinds and hit-rate paired with its false-positive bound on known inputs.
- [x] 1.3 Render the self-contained HTML and the text summary from `cmd/eventreport`; verify escaping, absence of scripts and external resources, and the command's exit status on empty input.

## 2. Operation and verification

- [x] 2.1 Add `make dashboard` over the Compose stack or a log file, writing under `loadtest/out/`; run it end to end against the local stack.
- [x] 2.2 Document the command, the views, the percentile method and the limits in `docs/operations.md`; run `make test`, `make lint` and `openspec validate --all`.
