## 1. Store-outage harness

- [ ] 1.1 Add three load-test API keys and opt-in policy and price variables to the Compose profile without changing the other recipes.
- [ ] 1.2 Add the k6 client, the orchestrator with hung and down arms and an always-restore trap, and `make chaos-redis`.
- [ ] 1.3 Add the verifier: request-level agreement, per-period table, fail-open by decision against warnings, overrun, lost spend against the store counter and recovery time.
- [ ] 1.4 Add fixture checks for the verifier and the orchestrator's stages to `make test-loadtest` and run mutation checks.

## 2. Event report

- [ ] 2.1 Count fail-open admissions from the cost event's `fail_open` field and update the tests, template and summary.

## 3. Measurement and documentation

- [ ] 3.1 Run both arms locally and publish `docs/measurements/redis-outage.md`.
- [ ] 3.2 Publish `docs/measurements/chaos-report.md` and link both reports.
- [ ] 3.3 Update `docs/operations.md`.

## 4. Verification

- [ ] 4.1 Run `make test`, `make lint`, `make test-loadtest`, shellcheck and OpenSpec validation.
