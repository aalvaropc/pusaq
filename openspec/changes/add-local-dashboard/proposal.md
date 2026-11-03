## Why

The gateway writes one JSON cost event per accounted request, plus shadow-cache, breaker and fail-open records, but nothing reads them back as a whole: per-tenant cost, latency per route and the shadow cache's numbers are only reachable through ad hoc `jq`. Operators of the local stack need these views from one command, at no cloud cost.

## What Changes

- New command `cmd/eventreport` reads the gateway's JSON log lines from a file or standard input, ignores lines that are not gateway records, optionally keeps one `run_id`, and writes one self-contained HTML report (inline CSS and SVG, no scripts or external resources) plus a short text summary on standard output.
- The report has six views: cost per tenant with estimated cost apart from measured cost; latency percentiles per route with a stated nearest-rank method; outcomes by error code and HTTP status; fallback and breaker activity; rate-limit denials by limit kind with fail-open admissions; and the shadow cache's predicted hit-rate always paired with its false-positive upper bound (fingerprint mismatches), with a τ sweep and the similarity distribution.
- New `make dashboard` target collects the records from the local Compose stack (default and `loadtest` profile services) or from an existing log file, and writes the report under the ignored `loadtest/out/` directory.
- `docs/operations.md` describes the command, each view, the percentile method and the limits of local mock data.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `operations`: adds the requirement for a local event report built from the gateway's own log records.

## Impact

New `cmd/eventreport` and `internal/report` packages (standard library only), a Makefile target and operations documentation. The gateway, its event schema and the load-test scripts are unchanged; the report is a read-only consumer of existing records.
