## Context

See proposal.md. Provider attempts are counted before invocation. Token settlement already runs in finalization with an independent bounded context.

## Goals / Non-Goals

Return reservations proven unused without changing provider interfaces, event fields, keys, configuration or fallback behavior. Inferring consumption from errors or HTTP status is out of scope.

## Decisions

Reuse the attempt count: nonzero usage reconciles normally; empty usage with zero attempts refunds the reservation; empty usage after an attempt keeps it. This avoids a second execution state or speculative provider error classification.

Keep one adjustment attempt in the registered finalizer and retain the original outcome if it fails. The admission charge remains the meaning of tokens_charged. Verify ledger conservation with actual Lua and frozen store time, then measure the existing local TPM profile.

## Risks / Trade-offs

Provider invocation without reported usage remains unknown and conservatively keeps the charge. A failed refund can under-admit; the existing warning records the adjustment failure without retrying an ambiguous write. No data migration is required.
