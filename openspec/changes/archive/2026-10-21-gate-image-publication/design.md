## Context

See proposal.md for the security boundary.

## Goals / Non-Goals

The change verifies repository controls without deploying cloud resources.

## Decisions

Use a reusable workflow called after ci-gate on push to main, scoped OIDC permissions, immutable commit tags, numeric identity conditions, and credential-free Terraform tests. Cloud deployment is out of scope.

## Risks / Trade-offs

External tooling requires network access; verification uses pinned versions.

GitHub self-repository references resolve the exact workflow commit. actionlint v1.7.12 does not recognize this supported syntax; its exclusion matches only the reusable publisher reference. Other workflow-call diagnostics remain enabled.
