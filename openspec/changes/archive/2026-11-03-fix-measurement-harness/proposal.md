## Why

The failover harness mixes outcomes that constitution rule 6 requires to be reported separately, and part of its arithmetic has no stated method. k6 status 0 counts as an interrupted stream even when no response byte arrived. Send time is completion minus k6's request duration, which excludes connection set-up. Percentiles take a floor index without interpolation, and the failure window parses only `Ns`/`Nm`. Readiness-window records and fail-open warnings are collected whatever run logged them, and cloud collection waits a fixed 30 s. The mock's `latency-p99` setting is the maximum of its distribution, not a percentile. Several reports also describe their configuration or recipe inaccurately.

## What Changes

- Streams end complete, interrupted, never started or refused; a stream that fails before its first response byte is never started and is retried once.
- The k6 client records each request's send instant; the verifier measures from it, interpolates percentiles linearly between closest ranks and parses Go durations.
- Records without a run id count only within the run's interval; an injected run needs exactly one readiness-window down record, and a control run none.
- Cloud collection polls Cloud Logging until the run's records arrive, bounded by a stated wait.
- The mock names its latency bound `-latency-max` (`-latency-p99` stays accepted); the distribution is unchanged.
- Reports and references correct the breaker probe wording, the lease-mode recipe, the failover mock latency and historical methods, the `make up` help and the tenant-policy conditions. Measured values are unchanged.

## Capabilities

### New Capabilities

### Modified Capabilities

- `resilience`: the failover measurement reports never-started streams apart from interrupted ones, measures times from recorded send instants with a stated percentile method, and scopes its evidence to the run.

## Impact

`loadtest/` scripts and the k6 chaos client, the mock provider's flags, Compose and Makefile mock arguments, and measurement and reference documentation. Gateway behavior is unchanged. `make test-loadtest` also requires Node.js.
