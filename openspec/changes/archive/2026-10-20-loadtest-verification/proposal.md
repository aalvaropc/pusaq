## Why

The limiter verifier prints gateway counts without asserting agreement, and failed k6 runs stop local recipes before their reports. Measurements need independent checks that fail on missing or contradictory evidence.

## What Changes

- Compare limiter admissions, rejections and unexpected outcomes against gateway events and mock counters.
- Compare chaos outcomes by tenant and region, separating responses that never enter the gateway pipeline.
- Preserve reports and failure statuses when execution, collection or verification fails.
- Freeze the measurement run id once per Make invocation.
- Exercise verification scripts with synthetic evidence in CI.

## Capabilities

### New Capabilities

### Modified Capabilities

- `rate-limiting`: limiter measurement evidence agrees across client, upstream and gateway.
- `resilience`: chaos evidence is cross-checked by tenant and region.
- `operations`: failed measurement commands retain their reports and failing exit status.

## Impact

Local measurement scripts, k6 metadata, Make recipes and CI. The gateway HTTP API is unchanged.
