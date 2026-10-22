## Context

See proposal.md for the security boundary.

## Goals / Non-Goals

The change enforces the specified security boundary without deploying cloud resources.

## Decisions

Require API keys unless PUSAQ_DEV_MODE=true; reject conflicting keys and dev mode. Preserve keyless local Compose by setting development mode only on the default development gateway. Load-test services keep their configured API keys.

## Risks / Trade-offs

Compatibility and resource boundaries are verified with focused regression tests.
