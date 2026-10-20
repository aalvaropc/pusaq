## Why

The development stack publishes its gateway, Redis and unauthenticated mock on every host interface. Local development needs host access without exposing these services to the surrounding network.

## What Changes

- Publish the existing ports only on IPv4 loopback.
- Preserve service names and container-to-container networking.
- Document explicit Compose overrides for access from another machine.

## Capabilities

### New Capabilities

### Modified Capabilities

- `operations`: the development stack publishes host ports on loopback only.

## Impact

Compose port bindings and local operations documentation. Host ports and HTTP contracts stay the same; access from another machine requires an explicit override.
