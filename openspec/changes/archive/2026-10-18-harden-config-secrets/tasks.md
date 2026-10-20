# Tasks: harden-config-secrets

## 1. Configuration

- [x] 1.1 Errors without keys or URLs; tests assert the secret is absent.
- [x] 1.2 Empty `PUSAQ_API_KEYS` fails startup.
- [x] 1.3 Upstream key-variable collisions fail startup.
- [x] 1.4 Fields trimmed in `PUSAQ_API_KEYS` and `PUSAQ_UPSTREAMS`.

## 2. Docs and delivery

- [x] 2.1 `docs/configuration.md`, `docs/gateway.md`.
- [x] 2.2 Suite with `-race`, lint, `openspec validate --all`; draft PR.
