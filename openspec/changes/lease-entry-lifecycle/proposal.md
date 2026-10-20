## Why

An admission can acquire tokens through an entry removed by cleanup, leaving them unreferenced. Cleanup can also block unrelated tenants by waiting for a tenant mutex while holding the map mutex.

## What Changes

- Revalidate entry ownership after acquiring its tenant mutex.
- Retire only idle current entries without waiting for a busy tenant under the map mutex.
- Specify that local denial caching ends at the smaller of store retry and lease TTL.
- Verify ledger conservation, tenant isolation and local strict/lease measurements.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `rate-limiting`: preserve lease ownership during cleanup, isolate tenant waits and bound local denial caching by lease TTL.

## Impact

Lease admission and cleanup, concurrency tests, timing documentation and measured reports. Public APIs, Redis scripts, keys, configuration and statistics retain their representation.
