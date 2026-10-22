## Context

See proposal.md for the security boundary.

## Goals / Non-Goals

The change enforces the specified security boundary without deploying cloud resources.

## Decisions

Load the transport payload lazily after authentication and before RPM. Keep payload parsing in a separate middleware, preserve request IDs, bound body reads and individual response writes, and close unread HTTP/1 connections on early rejection. Keep SSE free of a total lifetime timeout.

## Risks / Trade-offs

Compatibility and resource boundaries are verified with focused regression tests.
