# Tasks: add-limiter-load-test

## 1. Ground truth and off switch

- [x] 1.1 `internal/mockprovider`: request counter (total, injected errors, per model) and `GET /stats` JSON; tests: zero at start, per-model counts including streamed requests, injected errors counted, 100 concurrent requests under `-race`.
- [x] 1.2 `internal/config`: `PUSAQ_TPM_LIMIT=0` means disabled (negative still rejected; `PUSAQ_TPM_BURST` with a zero limit is a config error); `cmd/pusaq`: build the token limiter only when the limit is positive, warn at startup otherwise; config tests.

## 2. Harness

- [x] 2.1 `docker-compose.yml` profile `loadtest`: `pusaq-a`/`pusaq-b` (limited), `pusaq-nolimit-a`/`-b`, `mock-lt` (zero latency), `lb` (nginx, two listeners, no published ports), `k6` (pinned image, mounts `./loadtest`); `loadtest/nginx.conf`. `make up` unchanged.
- [x] 2.2 `loadtest/limiter.js`: scenarios `exactness`, `neighbor`, `toll_limited`, `toll_unlimited`; counters tagged by scenario; `/stats` delta in setup/teardown; thresholds on the bound, zero neighbor rejections, zero dropped iterations; `handleSummary` → `loadtest/out/<run_id>.json`.
- [x] 2.3 `loadtest/verify.sh` (bash + jq) cross-checking k6, mock, per-replica cost events, Redis script calls, fail-open count; `Makefile` targets `loadtest` (flush + reset stats + run + verify) and `loadtest-down`; `.gitignore` for `loadtest/out/`.

## 3. Measurement and delivery

- [x] 3.1 Run `make loadtest` with the default parameters and once with `LOADTEST_DURATION=300s`; fill `docs/measurements/limiter-exactness.md` (hypothesis, protocol, command, environment, results table, caveats).
- [x] 3.2 README: "Measurements" section linking the doc and the targets; `PUSAQ_TPM_LIMIT=0` documented in the configuration list.
- [x] 3.3 Full suite `-race` green; lint + vet clean; `make up && make smoke` still works; conventional commit(s) on `feat/limiter-load-test`, PR to main, CI green.
