## Why

Diagnostics must not retain sensitive dependency payloads or panic values.

## What Changes

Log safe failure categories and correlation metadata without raw response bodies or error strings. Preserve client and cost schemas. Verify synthetic secrets across buffered, streaming, fallback, embedding, and panic paths.

## Capabilities

### Modified Capabilities

- `cost-telemetry`: Diagnostics must not retain sensitive dependency payloads or panic values.

## Impact

Gateway, provider, and shadow-cache diagnostics.
