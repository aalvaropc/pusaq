# Tasks: fix-streaming-accounting

## 1. Usage

- [x] 1.1 Streamed provider calls carry `stream_options.include_usage`; buffered calls do not; the mock sends usage only when asked.
- [x] 1.2 Estimated cost from the reservation when usage is unknown after a provider answered; `cost_estimated` on the event; spend counts it.

## 2. Stream integrity

- [x] 2.1 Event scanner: all line terminators, multi-line data, flush at the end, 1 MiB event bound.
- [x] 2.2 Terminal error event on upstream failure or missing `[DONE]`; client disconnects write nothing.
- [x] 2.3 Started streams feed the breaker at the end.

## 3. Docs and delivery

- [x] 3.1 `docs/gateway.md`, `docs/design.md` §5.7.
- [x] 3.2 Suite with `-race`, lint, `openspec validate --all`; draft PR.
