## 1. Evidence and execution

- [x] 1.1 Assert limiter source agreement and run scoping; verify missing, duplicate and reclassified fixtures fail.
- [x] 1.2 Assert chaos source agreement by tenant and region; verify local equality, cloud main tolerance and non-comparable outcomes with fixtures.
- [x] 1.3 Preserve measurement execution, collection and verification status; verify every stage is attempted for each failure combination and RUN_ID is stable.

## 2. Validation

- [x] 2.1 Add a shell fixture target to CI and verify it without Docker.
- [x] 2.2 Run local limiter and chaos measurements, Go race tests, lint, shellcheck and OpenSpec validation; record counts and limitations.
