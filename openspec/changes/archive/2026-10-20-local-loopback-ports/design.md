## Context

The host needs ports 8080, 6379 and 9090 for local commands. Containers use service DNS names.

## Goals / Non-Goals

Local-only host access is the goal. Remote development access is out of scope.

## Decisions

Use explicit `127.0.0.1:host:container` bindings. Removing the Redis and mock ports breaks host workflows; keeping wildcard bindings exposes unauthenticated services. Container networking stays independent of host bindings.

## Risks / Trade-offs

Access from another host requires a deliberate Compose override. IPv6 loopback is not published.
