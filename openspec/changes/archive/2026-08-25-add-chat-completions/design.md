# Design: add-chat-completions

## Context

Foundation in place: transport-agnostic pipeline, accounting stage, async telemetry, normalized errors, mock provider serving OpenAI-shaped JSON. Constraints from CLAUDE.md: aliases only in code (rule 1), OpenAI subset with `X-Pusaq-Model` (rule 7), normalized errors, stdlib-first. See proposal.md for motivation.

## Goals / Non-Goals

**Goals:**

- The provider abstraction and alias-resolution pattern later slices extend (Gemini/Claude adapters, router stage).
- A working OpenAI-compatible endpoint an SDK can actually hit.

**Non-Goals:**

- SSE streaming, tool calling, multi-provider routing rules, tenants/auth, rate limiting — all later changes.
- Retries/fallback on upstream failure (arrives with the circuit-breaker slice); this slice fails fast with `upstream_error`.

## Decisions

1. **`internal/provider` owns the upstream contract.**
   `Provider` interface: `Complete(ctx, ChatRequest) (ChatResponse, error)` with domain types (not HTTP). `httpprovider` adapter POSTs the OpenAI-shaped JSON to a base URL with a bounded `http.Client` — the mock provider today, any OpenAI-compatible endpoint tomorrow. Alternative (call the mock from the gateway package directly) rejected: the interface seam is exactly where Vertex adapters and the router plug in.

2. **Completions types live in `internal/gateway` as the API subset.**
   Request: `model`, `messages` (role+content), `max_tokens`. Response: `id`, `object`, `model`, `choices[].message`, `choices[].finish_reason`, `usage`. Unknown request fields are ignored (OpenAI-compatible tolerance); missing/invalid required fields are `invalid_request`.

3. **Alias resolution is a config-owned routing table.**
   `config.Routes`: alias → {provider, model}. Defaults map `pusaq/fast|best|auto` → mock's `mock-model`; `PUSAQ_MOCK_URL` names the upstream base URL, `PUSAQ_UPSTREAM_TIMEOUT` bounds calls (default 30s). Physical model names live here only. The terminal handler receives the table and a provider registry (`map[string]provider.Provider`); with one provider the registry is trivial but establishes the seam the router stage will use.

4. **Upstream failures map to `upstream_error` (502).**
   New `core.Code`. Provider non-2xx and timeouts both produce it; the provider's raw body is logged, never forwarded (no leaking). Fail-fast, no retry — resilience is a later, measured slice.

5. **`X-Pusaq-Model` is set by the completions handler** once the alias resolves, on success responses; the request-scoped `core.Request` gains `RequestedModel`/`ServedModel` fields the accounting stage copies into the cost event (with usage). This keeps the accounting stage generic — it reads what the envelope accumulated, it doesn't know about completions.

6. **Echo endpoint removed** and the smoke target now exercises `/v1/chat/completions` against the mock. The foundation's pipeline specs are unaffected (they never named echo).

## Risks / Trade-offs

- **Envelope grows completion-specific fields** (models, usage). Accepted: they are accounting fields, and the alternative (per-op event subtypes) is speculative complexity now.
- **No retry on upstream failure** may look weak next to the design doc's fallback story. Accepted deliberately: fallback belongs to the breaker slice where it is measured, per the constitution's "measured verification".
- **Single provider registry entry** ("mock") until Vertex adapters land. Accepted: the seam, not the breadth, is this slice's deliverable.
