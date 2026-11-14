## 1. Store health gate

- [ ] 1.1 Add `Ticket.Probe` to `internal/breaker` with tests.
- [ ] 1.2 Add `internal/storegate`: the go-redis hook, failure classification, skip error and background PING probe, with deterministic tests (fake store, injected clock and probe runner), concurrency under `-race`, and a mutation check of the rule.
- [ ] 1.3 Cover the callers with a real client: strict and lease admission, token charge and reconciliation, budget read and increment, lease refunds; fail-open recorded, fail-closed refused without a store call, healthy call counts unchanged.
- [ ] 1.4 Wire the gate in `cmd/pusaq`: one gate per store client, `breaker_state` records with `component` `store`, the constants in the startup log.

## 2. Observability

- [ ] 2.1 Show store-gate transitions in the event report's rate-limiting view.
- [ ] 2.2 Collect gate transitions per replica in `loadtest/redis-outage.sh`; report detection and recovery per replica in `loadtest/verify-redis.sh` and fail on contradicting gate records; extend the fixtures in `make test-loadtest`.

## 3. Measurement and documentation

- [ ] 3.1 Run `make chaos-redis` (both arms, at least two runs) on the gated gateway and add a dated section to `docs/measurements/redis-outage.md`.
- [ ] 3.2 Update the store-outage case of `docs/measurements/chaos-report.md`, `docs/design.md` and `docs/operations.md`.

## 4. Verification

- [ ] 4.1 Run `make test`, `make lint`, `make test-loadtest` and `openspec validate --all`.
