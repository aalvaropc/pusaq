## Context

See proposal.md for the security boundary.

## Goals / Non-Goals

The change enforces the specified security boundary without deploying cloud resources.

## Decisions

Remove raw prompts from index entries, clone retained data, clear evicted references and enforce count plus byte LRU caps. Reserve a shared pending-sample byte budget across queue and workers, return credits on every path, and synchronize shutdown with offers. Defaults are 64 MiB index and 16 MiB pending data, not total RSS guarantees.

## Risks / Trade-offs

Compatibility and resource boundaries are verified with focused regression tests.
