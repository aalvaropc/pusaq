# Design: fix-upstream-paths

## Context

`provider.HTTP` appends `/v1/chat/completions` to the upstream URL; `embed.HTTP` appends `/embeddings`. `PUSAQ_EMBEDDER=openai:<name>` looks up a chat upstream and reuses its URL and key. `cmd/cachecal` already takes an embeddings base URL directly. See proposal.md.

## Goals / Non-Goals

**Goals:**

- One URL convention for every OpenAI-compatible call: the base URL includes the version, and each operation appends its path.
- An embedder that does not depend on the chat backends.

**Non-Goals:**

- Keeping the previous URL forms working: a URL without the version segment now reaches `<url>/chat/completions` and fails at the provider.

## Decisions

1. **The version belongs to the URL.** OpenAI SDKs, Gemini's OpenAI endpoint and self-hosted servers publish base URLs with their version, and the embedder and `cachecal` already follow that convention. Moving chat onto it removes the mismatch at its source.

2. **The embedder has its own URL and key.** It reads `openai:<url>` directly, with `PUSAQ_EMBED_KEY`, so embedding can use a provider that is not a chat backend, and a chat backend can be dropped without breaking the cache measurement. The URL is validated like an upstream URL, and errors do not echo it.

## Risks / Trade-offs

- Existing deployments must add the version segment to `PUSAQ_UPSTREAMS`. The Compose files and the Terraform runtime are updated in this change; any other environment fails with provider 404s on its first request, which the cost events record as `upstream_error`.
