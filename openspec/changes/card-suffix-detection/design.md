## Context

See proposal.md. The existing bounded regex returns a greedy 13–19 digit candidate. Its checksum can include a separated suffix, although a shorter candidate ending at a separator validates.

## Goals / Non-Goals

Recover supported card shapes without weakening checksum, issuer or contiguous-number boundaries. Broader issuer validation, other policy families and serving cached responses are out of scope.

## Decisions

Keep the regex and examine prefixes ending at the match boundary or before a supported space/hyphen separator. Validate each candidate using the existing 13–19 digit, issuer-prefix and Luhn rules. There are at most seven candidate lengths; no combinatorial search or new dependency is needed. Never cut within uninterrupted digits.

Verify exact card flags, block/log behavior and shadow exclusion for user and system messages in buffered and streaming requests. Extend the original corpus with 15 targeted synthetic positives and 13 hard negatives and keep all existing floors.

## Risks / Trade-offs

These are heuristic matches, not issuer authenticity checks. The targeted regression rows are not blind traffic and do not establish production precision. Unseparated appended digits are not split into a card and suffix. The original injection and temporal outcomes remain the control.
