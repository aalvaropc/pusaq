# Proposal: fix-streaming-accounting

## Why

Streams are accounted only because the mock provider always sends a usage chunk:
- **Usage is never requested.** Pusaq does not ask for one (`stream_options.include_usage`), so a real provider with OpenAI semantics would stream without usage, leaving the token charge unreconciled, the event without cost, and the monthly budget blind to streaming.
- **A stream without usage adds $0 to the budget,** whatever the reason.
- **A provider failure mid-stream is invisible to the client:** it gets a 200 that ends cleanly without `[DONE]` or an error event, while the gateway records `upstream_error`.
- **The usage scanner** only splits lines on `\n`, so carriage-return line endings, multi-line `data:` fields and a final `[DONE]` without a newline lose the usage or the completion mark.
- **A stream that ends without `[DONE]`** counts as a success.
- **The scanner's buffer has no bound.**
- **Mid-stream failures never reach the backend's breaker.**

## What Changes

- **Streamed provider calls** ask for usage (`stream_options: {include_usage: true}`); buffered calls never carry the option. The mock sends its usage chunk only when asked.
- **When usage is unknown after a provider answered** (no usage chunk, client abort, mid-stream failure), the request is priced from its reservation — the input estimate plus `max_tokens` or the default reservation — and the cost event marks it `cost_estimated`; the monthly spend counts that cost.
- **The scanner reads events, not lines:**
  - `\n`, `\r\n` and `\r` end a line, `data:` lines join into one event, and the pending event is flushed at the end.
  - An event over 1 MiB ends the stream as an upstream failure.
- **A stream that fails upstream or ends without `[DONE]`** receives one terminal SSE error event in the normalized error shape and ends as `upstream_error`. A client disconnect writes nothing.
- **A started stream feeds its backend's breaker when it ends:** a failure as a failure, a completed stream as a success, a client disconnect as no outcome.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `chat-completions`: streams request usage, report mid-stream failures in-band, and complete only with `[DONE]`.
- `cost-telemetry`: estimated cost when usage is unknown after a provider answered.
- `tenant-policy`: the monthly spend counts estimated costs.
- `resilience`: a started stream's outcome feeds its backend's breaker.

## Impact

- **Modified:**
  - `internal/provider/{http,provider}.go`, `internal/mockprovider/server.go`;
  - `internal/gateway/completions.go`, `internal/core/cost.go`, `internal/telemetry/emitter.go`;
  - `docs/gateway.md`, `docs/design.md`.
- **Unchanged:** no new dependencies, no new configuration.
