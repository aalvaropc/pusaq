## Context

See proposal.md for the security boundary.

## Goals / Non-Goals

The change verifies repository controls without deploying cloud resources.

## Decisions

Run dependency reachability, workflow analysis, CodeQL, dependency review, and scans of both container targets. Aggregate required jobs in ci-gate. No cloud credentials are needed.

## Risks / Trade-offs

External tooling requires network access; verification uses pinned versions.
