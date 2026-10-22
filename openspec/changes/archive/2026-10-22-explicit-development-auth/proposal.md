## Why

Unauthenticated operation requires an explicit development setting.

## What Changes

Require API keys unless PUSAQ_DEV_MODE=true; reject conflicting keys and dev mode. Preserve keyless local Compose by setting development mode only on the default development gateway. Load-test services keep their configured API keys.

## Capabilities

### Modified Capabilities

- `tenant-auth`: Unauthenticated operation requires an explicit development setting.

## Impact

Gateway behavior, configuration, and its specifications.
