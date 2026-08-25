# Tasks: add-sse-streaming

## 1. Core and provider

- [x] 1.1 `internal/core`: `Response.Stream io.ReadCloser` and the `stream_interrupted` code; verify build and existing tests.
- [x] 1.2 `internal/provider`: `Stream bool` on `ChatRequest`; `StreamComplete(ctx, req) (io.ReadCloser, error)` on the interface; HTTP adapter stream client (`ResponseHeaderTimeout`, no overall deadline), non-200 before body → `upstream_error`; tests: streamed round-trip bytes pass through, 500 → upstream_error, header timeout enforced.

## 2. Gateway

- [x] 2.1 Accounting stage: finalizing stream wrapper — emit exactly once at EOF (success) / read error (upstream_error) / early Close (stream_interrupted); deferred emission skipped for stream responses; tests for all three endings plus buffered regression.
- [x] 2.2 Completions handler streaming path: `stream: true` → provider `StreamComplete`, usage-scanning passthrough onto the envelope, SSE headers on the domain response; pre-stream failures stay normalized JSON errors; tests: chunk fidelity, usage captured, unknown model still 400.
- [x] 2.3 HTTP adapter: when `Stream` is non-nil, write headers and copy with flush per read via `http.NewResponseController`, closing the stream; httptest e2e: `text/event-stream`, chunks + `[DONE]` delivered, exactly one cost event after the body is consumed, with usage.

## 3. Mock provider

- [x] 3.1 `"stream": true` handling: word chunks in `chat.completion.chunk` shape, final usage chunk, `data: [DONE]`, configured latency before first chunk; tests assert framing and termination.

## 4. Verification and delivery

- [x] 4.1 Full suite `-race` green; lint + vet clean.
- [x] 4.2 E2E with real binaries: `curl -N` streamed completion shows chunks then `[DONE]`; cost event appears only at stream end with usage; buffered path unchanged.
- [x] 4.3 README API section documents `stream: true`.
- [x] 4.4 Conventional commit(s) on `feat/sse-streaming`, push, PR to `main`; CI green.
