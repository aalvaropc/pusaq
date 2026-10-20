## Why

A gateway replica can move a bucket timestamp backward, so another replica credits elapsed time repeatedly. A shared bucket needs a single time source for refill decisions.

## What Changes

- Read Redis time inside each atomic bucket operation.
- Preserve the maximum stored timestamp when the store clock moves backward.
- Share refill and persistence logic across admission, adjustment and lease acquisition.
- Test store-clock control, rollback and concurrent replicas; update the timing contract documentation.

## Capabilities

### New Capabilities

### Modified Capabilities

- `rate-limiting`: store time governs refill and bucket timestamps never move backward.

## Impact

Internal Lua arguments and test clock injection change. Public Go interfaces, configuration, keys and stored bucket fields remain the same. Existing timestamps ahead of store time conservatively delay refill until the store catches up.
