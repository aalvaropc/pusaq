## Why

Stream validation must bound memory and prevent raw provider errors from escaping.

## What Changes

Validate one complete SSE event at a time with a 1 MiB raw framing limit. Preserve valid event bytes, hash content incrementally, withhold error events, and preserve cancellation, accounting, breaker, and shadow semantics.

## Capabilities

### Modified Capabilities

- `chat-completions`: Stream validation must bound memory and prevent raw provider errors from escaping.

## Impact

Gateway behavior, configuration, and its specifications.
