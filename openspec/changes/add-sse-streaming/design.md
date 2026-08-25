# Design: add-sse-streaming

## Context

Buffered completions work end-to-end; the accounting stage emits the cost event in a `defer` when the pipeline call returns. A streamed response outlives that call — the body is still being copied to the client after the handler returns — so emission timing and usage capture must move to stream end without breaking the pipeline's transport-agnostic shape. See proposal.md.

## Goals / Non-Goals

**Goals:**

- SSE passthrough with the exactly-once accounting invariant intact.
- Streaming support in the provider seam without breaking the buffered path.

**Non-Goals:**

- Client-requested `stream_options`; TPM reservation/reconciliation (limiter slices); retry/fallback mid-stream.

## Decisions

1. **`core.Response.Stream io.ReadCloser`** (nil for buffered responses). The domain stays transport-agnostic: a stream is just a byte source; only the HTTP adapter knows about SSE framing, flushing, and client disconnects. Alternative (callback-based `CompleteStream(fn)`) rejected: inverts control for no gain and complicates the accounting wrapper.

2. **Accounting finalizes at stream end.** When the handler returns a response with a non-nil `Stream`, the accounting stage swaps it for a finalizing wrapper and skips the deferred emission. The wrapper emits exactly once: on EOF (success), on read error (`upstream_error`), or on Close-before-EOF (`stream_interrupted`, the client left). Latency then covers the full stream — which is the number that matters for LLM traffic. The panic/error paths are untouched (an error response never carries a stream).

3. **Usage is parsed from the stream by a scanning passthrough.** The completions handler wraps the provider stream in a line-scanner that forwards bytes untouched while watching `data:` events for a `usage` object (the final chunk carries it, as OpenAI upstreams do with usage reporting enabled); found usage lands on the envelope, which the accounting wrapper reads at finalize. Passthrough fidelity over re-serialization: Pusaq never rewrites provider chunks.

4. **Two HTTP clients in the provider adapter.** The buffered client keeps its whole-exchange timeout; streams use a client with `ResponseHeaderTimeout` (time to first byte bounded by the upstream timeout) and no overall deadline — a whole-exchange timeout would kill any generation longer than the limit. Context cancellation still aborts abandoned streams.

5. **HTTP adapter streams with flush-per-read** via `http.NewResponseController`; SSE headers (`text/event-stream`, `no-cache`) set by the completions handler on the domain response, honored by the adapter.

6. **Mock provider streams deterministically:** canned content split into word chunks (`chat.completion.chunk` shape), a final chunk carrying usage, then `data: [DONE]`, with the configured latency before the first chunk and a small fixed inter-chunk delay.

## Risks / Trade-offs

- **Usage depends on the upstream emitting it in-stream.** Accepted: the mock does, OpenAI-compatible upstreams can; absent usage yields an event without token counts rather than a failure. Estimation belongs to the TPM slice.
- **A malformed final chunk loses usage silently.** Accepted for this slice; the scanner logs nothing on parse misses to stay off the hot path. The TPM slice adds reconciliation pressure that will surface gaps.
- **`ResponseHeaderTimeout`-only bounding means a stalled mid-stream upstream holds a connection** until the client gives up. Accepted now; per-chunk idle timeouts arrive with the resilience slice.
