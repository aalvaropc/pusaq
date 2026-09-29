# Tasks: add-tpm-overrun-measurement

## 1. Configuration

- [x] 1.1 `internal/config`: an empty `PUSAQ_TPM_BURST` means the default; test row.

## 2. Harness

- [x] 2.1 Compose: `LOADTEST_TPM_LIMIT`, `LOADTEST_TPM_BURST`, `LOADTEST_MOCK_LATENCY` knobs (defaults keep `loadtest` and `breakertest` unchanged); k6 env `TPM_*`; Makefile `tpmtest` with target-specific values (token limit 6000/min, burst 1000, 20 rps per arm, 60 s, request limit raised, 2 s mock latency).
- [x] 2.2 `loadtest/tpm.js`: `under` and `over` arms (two tenants, two aliases, `max_tokens` 8 and 64), counters and mock cross-check, bound in tokens with the under-reservation ceiling, summary to `out/<run_id>-tpm.json`.
- [x] 2.3 `loadtest/verify-tpm.sh`: per arm from cost events — admitted (vs k6 and mock), Σcharged, Σactual, error per request, end and peak overrun, max in flight; reconciliation failures and fail-open must be zero; FAIL rules.

## 3. Measurement and delivery

- [x] 3.1 Run `make tpmtest` (2 s latency) and `make tpmtest TPM_MOCK_LATENCY=0` (control); write `docs/measurements/tpm-overrun.md`.
- [x] 3.2 README (Measurements bullet, token-limit paragraph, `PUSAQ_TPM_BURST` note), `docs/design.md` §5.3 and §8, one line in `limiter-exactness.md`.
- [x] 3.3 Full suite `-race` green; lint + vet clean; `make loadtest` and `make breakertest` still pass at their defaults; conventional commit(s) on `feat/tpm-overrun-measurement`, draft PR.
