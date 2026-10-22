## Context

See proposal.md for the security boundary.

## Goals / Non-Goals

The change enforces the specified security boundary without deploying cloud resources.

## Decisions

Validate provider and embedding base URLs, require HTTPS for credentials except explicit loopback development, and disable redirects in all outbound HTTP clients. Keep keyless HTTP mocks and versioned HTTPS bases compatible.

## Risks / Trade-offs

Compatibility and resource boundaries are verified with focused regression tests.
