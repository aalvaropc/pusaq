## Context

See proposal.md for the security boundary.

## Goals / Non-Goals

The change minimizes retained diagnostic data while preserving normalized failures and accounting.

## Decisions

Log safe failure categories and correlation metadata without raw response bodies or error strings. Preserve client and cost schemas. Verify synthetic secrets across buffered, streaming, fallback, embedding, and panic paths.

## Risks / Trade-offs

Removing raw errors reduces detail; safe categories, status codes, and correlation IDs retain diagnostic context.
