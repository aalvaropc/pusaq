# Tasks: add-breaker-measurement

## 1. Observability and configuration

- [x] 1.1 `internal/breaker`: `Config.OnStateChange(from, to)` invoked outside the lock on every transition; tests: exact transition sequence through open, half-open, reopen, and close; the hook may inspect the breaker.
- [x] 1.2 `internal/config`: `PUSAQ_BREAKER_WINDOW`, `_MIN_SAMPLES`, `_THRESHOLD`, `_COOLDOWN`, `_PROBES` with defaults from the breaker package and validation; `cmd/pusaq`: breakers built from configuration, `breaker_state` log per transition, startup log with the five values; tests for defaults, overrides, and rejections.

## 2. Failure window

- [x] 2.1 `internal/mockprovider`: clock seam, `POST /admin/fail?for=<duration>` and `DELETE /admin/fail`, `failing` in `/stats`, every completion fails inside the window, window start and clear logged with instants; tests: fails until the window ends, clear, bad durations rejected, stats report failing.

## 3. Run and delivery

- [x] 3.1 Harness: `mock-lt-backup` and the fallback in the load-test profile, `BREAKER_*` parameters, `loadtest/breaker.js` (three tenants under their limits, flip at the warm-up instant, no 502 allowed), `loadtest/verify-breaker.sh`, `make breakertest`.
- [x] 3.2 Run `make breakertest` with a 5 s and a 15 s failure window; write `docs/measurements/breaker-detection.md` (claim, formula, protocol, environment, results, caveats).
- [x] 3.3 README (resilience paragraph: variables and the `breaker_state` record; measurements list; mock admin endpoints) and `docs/design.md` §5.6 and §8.
- [x] 3.4 Full suite `-race` green; lint + vet clean; `make loadtest` still passes; conventional commit(s) on `feat/breaker-measurement`, draft PR stacked on `feat/feature-routing`.
