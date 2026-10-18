# Proposal: fix-upstream-paths

## Why

The chat adapter builds `<url>/v1/chat/completions` from an upstream URL, while the embedder of the shadow cache builds `<url>/embeddings` from the same URL. No single value serves both:
- With the mock's `http://host:9090`, embeddings go to `/embeddings`, which the API does not serve.
- With an OpenAI-compatible base that carries its version, such as `https://generativelanguage.googleapis.com/v1beta/openai`, chat goes to `/v1beta/openai/v1/chat/completions`, which does not exist.

The embedder also borrows a chat upstream by name, so embedding through a provider forces it into the chat fallback chain.

## What Changes

- **Upstream URLs are API base URLs with the version included**, as OpenAI SDKs take them (`http://host:9090/v1`, `https://…/v1beta/openai`). Chat calls go to `<url>/chat/completions` and embeddings to `<url>/embeddings`.
- **The default upstream, the Compose files and the measurement runtime** carry `/v1`.
- **The embedder is configured on its own:**
  - `PUSAQ_EMBEDDER=local|openai:<url>`, with its bearer key in `PUSAQ_EMBED_KEY` and its model in `PUSAQ_EMBED_MODEL`. It no longer names a chat upstream.
  - A key without an `openai:` embedder fails startup.
  - Embedder errors never echo the URL.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `chat-completions`: upstream base URLs include the API version.
- `semantic-cache`: the embedder has its own URL and key, outside the chat chain.

## Impact

- **Modified:**
  - `internal/provider/http.go`, `internal/config/config.go`, `cmd/pusaq/main.go`;
  - `docker-compose.yml`, `infra/terraform/runtime/gateway.tf`;
  - `docs/configuration.md`, `docs/gateway.md`.
- **BREAKING (configuration):**
  - `PUSAQ_UPSTREAMS` URLs gain the version segment.
  - `PUSAQ_EMBEDDER=openai:<upstream>` becomes `openai:<url>` plus `PUSAQ_EMBED_KEY`.
- **Unchanged:** no new dependencies.
