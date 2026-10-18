# Tasks: fix-upstream-paths

## 1. Paths

- [ ] 1.1 Chat calls go to `<url>/chat/completions`; tests against a `/v1` and a `/v1beta/openai` base.
- [ ] 1.2 Default upstream, Compose and the Terraform runtime carry `/v1`.

## 2. Embedder

- [ ] 2.1 `PUSAQ_EMBEDDER=local|openai:<url>`, `PUSAQ_EMBED_KEY`; errors never echo the URL; a key without an `openai:` embedder fails startup.

## 3. Docs and delivery

- [ ] 3.1 `docs/configuration.md`, `docs/gateway.md`.
- [ ] 3.2 Suite with `-race`, lint, `openspec validate --all`, `make loadtest`; draft PR.
