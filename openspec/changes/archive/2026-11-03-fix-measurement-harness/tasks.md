## 1. Mock provider

- [x] 1.1 Name the latency bound `-latency-max`, keep `-latency-p99` accepted, and test that the configured maximum bounds every sample while the realized p99 stays below it.

## 2. Chaos harness

- [x] 2.1 Classify streams as complete, interrupted, never started or refused in a shared client module; retry never-started streams once; add Node.js fixture checks.
- [x] 2.2 Record send instants and measure from them; interpolate percentiles; parse Go durations in the client and the verifier; add fixture checks.
- [x] 2.3 Scope records without a run id to the run interval and require exactly one down record per injected run; add local and cloud fixture checks.
- [x] 2.4 Replace the fixed Cloud Logging wait with a bounded poll; add fixture checks for lagging logs, the bound and a failed k6 run.
- [x] 2.5 Report breaker half-open episodes under that name; add a fixture check.

## 3. Documentation

- [x] 3.1 Correct the breaker probe wording, lease-mode recipe, failover mock latency and historical methods, `make up` help, tenant-policy conditions and operations notes without changing measured values.

## 4. Verification

- [x] 4.1 Run mutation checks, `make test`, `make lint`, `make test-loadtest`, shellcheck, OpenSpec validation and a local chaos run; record the numbers.
