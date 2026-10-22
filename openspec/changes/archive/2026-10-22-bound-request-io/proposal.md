## Why

Authentication and I/O deadlines bound untrusted client work.

## What Changes

Load the transport payload lazily after authentication and before RPM. Keep payload parsing in a separate middleware, preserve request IDs, bound body reads and individual response writes, and close unread HTTP/1 connections on early rejection. Keep SSE free of a total lifetime timeout.

## Capabilities

### Modified Capabilities

- `request-pipeline`: Authentication and I/O deadlines bound untrusted client work.

- `cost-telemetry`: Payload-derived run IDs are present only after authenticated parsing.

## Impact

Gateway behavior, configuration, and its specifications.
