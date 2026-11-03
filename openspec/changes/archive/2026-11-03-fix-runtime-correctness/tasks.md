## 1. Breaker

- [x] 1.1 Hand out a generation ticket from `Allow`; apply `Record` and `Release` only for tickets of the current generation.
- [x] 1.2 Record and release through the ticket in the gateway's fallback chain, buffered success and stream settlement.
- [x] 1.3 Regression tests: a straggler neither closes nor reopens half-open, a stale release frees no probe slot, a stale outcome stays out of the recovered window, and a gateway stream admitted before an open does not decide the half-open breaker.

## 2. Telemetry

- [x] 2.1 Make `Emit` and `Close` exclusive so no accepted event is lost; regression tests for an `Emit` in progress during `Close` and for concurrent emitters racing `Close`.
- [x] 2.2 Write `attempts` on every event; regression test for an exhausted fallback chain.
- [x] 2.3 Record fail-open admissions (`rpm`, `tpm`, `budget`) in `fail_open`; regression tests for each and for the absence of the field when the store answers.
- [x] 2.4 Store latency as integer milliseconds; regression test that JSON encoding and the log sink agree.

## 3. Connection pool

- [x] 3.1 Give the buffered, streaming and embedding clients a 256-connection idle pool per host, keeping redirects disabled and every timeout.
- [x] 3.2 Regression tests counting new connections on each client for steady load (400 requests at concurrency 20) and bursts (10 bursts of 150); record before and after counts.

## 4. Calibration

- [x] 4.1 Select τ* by the one-sided Clopper–Pearson lower bound on precision; report undefined precision, recall and F1 as undefined.
- [x] 4.2 Add `-confidence` to `cmd/cachecal`, validate it, add support columns to the curve, and explain a failure.
- [x] 4.3 Regression tests: one predicted hit does not certify, sufficient support certifies, closed-form bound, undefined values, CSV output.
- [x] 4.4 Run `make calibrate` on the seed dataset and record its output.

## 5. Docs and verification

- [x] 5.1 Update `docs/gateway.md`, `docs/design.md`, `docs/configuration.md` and `docs/operations.md`.
- [x] 5.2 Mutation check of each new behavior; reproduction tests no longer reproduce.
- [x] 5.3 `make test`, `make lint` and `openspec validate --all` pass.
