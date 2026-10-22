## Context

See proposal.md for the security boundary.

## Goals / Non-Goals

The change verifies repository controls without deploying cloud resources.

## Decisions

Pin Actions by full commit SHA and container bases by verified registry digest. Use read-only checkout credentials, bounded jobs, weekly dependency updates, and private security reporting instructions.

## Risks / Trade-offs

External tooling requires network access; verification uses pinned versions.
