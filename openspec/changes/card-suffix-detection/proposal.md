## Why

A greedy card candidate can include expiry or CVV digits and fail its checksum, hiding a valid card. This weakens sensitive-data blocking and shadow-cache exclusion.

## What Changes

- Test bounded card candidates ending at an existing separator or the match boundary.
- Preserve issuer-prefix and Luhn checks without truncating uninterrupted digit blocks.
- Add suffix, checksum, issuer and boundary controls plus gateway policy regressions.
- Expand the labeled corpus and publish measured precision and recall.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `tenant-policy`: detect supported card shapes followed by separated numeric suffixes while preserving redaction and per-tenant actions.

## Impact

Sensitive-card matching, policy and gateway tests, corpus data and its measured report. No public API, configuration, dependency or other classifier family changes.
