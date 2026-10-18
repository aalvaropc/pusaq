# Tasks: harden-config-secrets

## 1. Configuration

- [ ] 1.1 Errors without keys or URLs; tests assert the secret is absent.
- [ ] 1.2 Empty `PUSAQ_API_KEYS` fails startup.
- [ ] 1.3 Upstream key-variable collisions fail startup.
- [ ] 1.4 Fields trimmed in `PUSAQ_API_KEYS` and `PUSAQ_UPSTREAMS`.

## 2. Docs and delivery

- [ ] 2.1 `docs/configuration.md`, `docs/gateway.md`.
- [ ] 2.2 Suite with `-race`, lint, `openspec validate --all`; draft PR.
