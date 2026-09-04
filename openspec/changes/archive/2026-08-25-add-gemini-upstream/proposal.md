# Proposal: add-gemini-upstream

## Why

Every capability so far — pipeline, streaming, tenants, RPM/TPM, breakers, fallback — has been proven against the mock. The product contract is "change your base_url and it works" against a **real model**. Google exposes Gemini through an OpenAI-compatible endpoint, which is exactly the seam Pusaq already speaks: this change adds authenticated upstreams and configurable per-alias models, and Gemini becomes a config entry — no new adapter code.

## What Changes

- **Authenticated upstreams**: each upstream may carry an API key (`PUSAQ_UPSTREAM_KEY_<NAME>`, uppercased upstream name); the HTTP adapter sends `Authorization: Bearer <key>` on buffered and streamed calls when present. Keys never appear in logs or cost events.
- **Configurable per-alias route models**: `PUSAQ_ROUTES="pusaq/fast=<model>,pusaq/best=<model>,pusaq/auto=<model>"` overrides the mock defaults; unknown aliases in the value are rejected at startup. Physical model names remain configuration-only (constitution rule 1).
- **Gemini as configuration**: `PUSAQ_UPSTREAMS="gemini=https://generativelanguage.googleapis.com/v1beta/openai,mock=http://localhost:9090"` plus a key and route models yields real completions through the whole pipeline — limits, breakers, fallback (mock as the fallback backend), streaming, and cost events with real token usage.
- README: a "Serving a real model" section with the exact env recipe.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `chat-completions`: model aliases resolve to configurable physical models per alias (previously fixed defaults); upstream calls are authenticated when the backend requires it.

## Impact

- Modified: `internal/config` (upstream keys, `PUSAQ_ROUTES`), `internal/provider` (auth header), `cmd/pusaq` (route models per alias), README, docker-compose (unchanged behavior — mock remains the default).
- No new dependencies; no new adapter code — the OpenAI-compatible seam holds.
