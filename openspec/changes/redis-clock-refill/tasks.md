## 1. Shared clock

- [ ] 1.1 Add store-time, rollback, existing-state and concurrency regression tests and demonstrate the original clock behavior fails them.
- [ ] 1.2 Use shared Lua refill/persistence and store time in all bucket operations; adapt deterministic fixtures and pass race tests.
- [ ] 1.3 Align timing documentation and verify unchanged public interfaces and stored fields.

## 2. Measurements

- [ ] 2.1 Run strict, lease, TPM, breaker and local chaos measurements with independent verification; record their counts and conditions.
- [ ] 2.2 Run all tests, shell fixtures, lint, shellcheck, build and OpenSpec validation.
