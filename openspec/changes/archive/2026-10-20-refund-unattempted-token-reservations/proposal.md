## Why

A request that never invokes a provider still consumes its full TPM reservation. The gateway can prove that these requests consumed no provider tokens and should return their reservations.

## What Changes

- Refund confirmed reservations when no provider was invoked.
- Retain charges when a provider was invoked and its consumption remains unknown.
- Preserve existing usage reconciliation, event fields and bounded finalization.
- Verify exact balances, error paths and the local TPM measurement.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `rate-limiting`: distinguish zero provider attempts from unknown consumption when settling reservations.

## Impact

Gateway token finalization, regression tests and measured documentation. No public interface, configuration, dependency or bucket representation changes.
