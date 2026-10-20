## ADDED Requirements

### Requirement: Development ports are local to the host

The default Compose development stack SHALL publish the gateway, Redis and mock-provider ports only on IPv4 loopback, preserving their existing host ports and service-to-service connectivity.

#### Scenario: Default host port bindings

- **WHEN** the default Compose configuration is resolved
- **THEN** every published service port binds to `127.0.0.1` and none binds to all interfaces

#### Scenario: Local completion and fallback

- **WHEN** the development stack is running and the primary mock is failed through its admin endpoint
- **THEN** a completion sent from the host reaches the fallback and returns successfully
