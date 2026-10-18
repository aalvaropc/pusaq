# Design: add-gemini-upstream

## Context

The provider seam (`internal/provider/http.go`) already speaks the OpenAI-compatible surface with two clients (buffered/stream); `PUSAQ_UPSTREAMS` defines the backend chain; routes are hardcoded to the mock's model. Google serves Gemini on an OpenAI-compatible endpoint with bearer-key auth. See proposal.md.

## Goals / Non-Goals

**Goals:**

- A real model behind Pusaq with zero new adapter code — the seam was the point.
- Secrets handled like tenant keys: env-only, never logged, fail-fast validation.

**Non-Goals:**

- Vertex AI (service-account) auth — that is a cloud-deployment concern; the Gemini API key path is the local/dev route.
- Per-alias primary *providers* (all aliases share the chain's primary).
- Model capability validation (Pusaq forwards; the upstream rejects models it doesn't serve — normalized as `upstream_error`).

## Decisions

1. **Keys via `PUSAQ_UPSTREAM_KEY_<NAME>`** (name uppercased, non-alphanumerics → `_`), not inline in `PUSAQ_UPSTREAMS`: keys don't belong in the same var as topology (visible in process listings and compose files that get committed). Key for an unknown upstream name → startup error (config bug, fail fast).

2. **`provider.NewHTTP` grows an option, not a signature break**: `WithAPIKey(key)` functional option (or a small `Options` struct — pick the lighter diff at implementation) setting `Authorization: Bearer` on both clients' requests. The header is set per-request, not stored in a shared http.Header (avoid mutation races).

3. **`PUSAQ_ROUTES="alias=model,..."`** overrides model names only, alias set stays fixed (the three public aliases are the product contract; new aliases are a product decision, not config). Unknown alias in the value → startup error. Defaults unchanged (mock-model) so the local experience needs zero config.

4. **E2E is honest about credentials**: with a user-provided Gemini key, the run proves a real completion (buffered + streamed) through limits/breakers/accounting with real usage; without one, the mock E2E stands and the README documents the recipe. The key is read from the environment at run time and never written to any file.

## Risks / Trade-offs

- **Google's OpenAI-compat surface may drift** from the subset Pusaq emits (e.g. usage-in-stream needs `stream_options` on some backends). Mitigated: E2E against the real endpoint; a missing usage chunk degrades to charge-stands semantics already spec'd, not an error.
- **A keyed fallback chain mixing real + mock** can serve mock answers on failover during local runs. Deliberate: that exercises the failover path; production chains would list only real backends.
