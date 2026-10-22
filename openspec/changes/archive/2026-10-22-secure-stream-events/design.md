## Context

See proposal.md for the security boundary.

## Goals / Non-Goals

The change enforces the specified security boundary without deploying cloud resources.

## Decisions

Validate one complete SSE event at a time with a 1 MiB raw framing limit. Preserve valid event bytes, hash content incrementally, withhold error events, and preserve cancellation, accounting, breaker, and shadow semantics.

## Risks / Trade-offs

Compatibility and resource boundaries are verified with focused regression tests.
