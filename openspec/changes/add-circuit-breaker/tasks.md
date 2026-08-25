# Tasks: add-circuit-breaker

## 1. Breaker

- [x] 1.1 `internal/breaker`: three-state machine with sliding window, minimum samples, cooldown (injected clock), bounded half-open probes; concurrency-safe. Tests: opens at threshold with min samples, never below min samples, fail-fast while open, cooldown → limited probes, probe success closes / failure reopens, race-free under `-race` with concurrent Allow/Record.

## 2. Fallback flow

- [x] 2.1 `internal/core`: `ServedProvider`/`Attempts` on the envelope and cost events; sink attributes.
- [x] 2.2 Completions flow: per-route fallback chain; skip open breakers without calling; record every attempt outcome; serve from the first success; all-failed → 502; non-upstream errors stop the chain. Buffered + pre-first-byte streaming. Tests with scripted fake providers: failover serves, open primary skipped (never called), all-down 502 with attempts on the event, mid-stream failure not retried, validation errors don't consume attempts.

## 3. Config, wiring, environment

- [x] 3.1 `internal/config`: `PUSAQ_UPSTREAMS` name=url list (first primary, rest fallbacks; duplicates/malformed rejected), replacing `PUSAQ_MOCK_URL`; default single local mock; tests.
- [x] 3.2 `internal/mockprovider`: echo the requested model in buffered and streamed responses; tests updated.
- [x] 3.3 `cmd/pusaq`: build providers + breakers from upstreams, routes with fallback chains; compose runs a second mock (`backup`) wired as fallback; README resilience section + config table.

## 4. Verification and delivery

- [x] 4.1 Full suite `-race` green; lint + vet clean.
- [x] 4.2 E2E in Compose: stop the primary mock mid-traffic → requests keep succeeding via `backup` (events show `served_provider:"backup"`, attempts 2, then attempts 1 once the breaker opens and fail-fast kicks in); restart the primary → half-open probes close the breaker and traffic returns.
- [x] 4.3 Conventional commit(s) on `feat/circuit-breaker`, push, PR (stacked on `feat/token-limits`); CI green.
