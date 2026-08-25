# Tasks: add-tenant-auth

## 1. Core and config

- [x] 1.1 `internal/core`: `APIKey`/`Tenant` on the envelope, `Tenant` on `CostEvent`, `unauthorized` code (401); build + existing tests green.
- [x] 1.2 `internal/config`: parse `PUSAQ_API_KEYS` (`key:tenant` pairs) into a map; reject malformed pairs, empty keys/tenants, and duplicate keys at startup; table-driven tests.

## 2. Gateway

- [x] 2.1 Auth stage: with keys configured — resolve bearer → tenant or reject `unauthorized` (401) without calling downstream; without keys — dev mode (`dev` tenant); tests: valid, missing, unknown, dev mode, and that rejections still emit cost events.
- [x] 2.2 HTTP adapter extracts `Authorization: Bearer` onto the envelope; accounting copies tenant into events; `/healthz` and `/readyz` stay unauthenticated; httptest e2e: 401 without key, 200 with key, tenant on both events.
- [x] 2.3 `internal/telemetry`: tenant attribute on the slog sink.

## 3. Wiring and delivery

- [x] 3.1 `cmd/pusaq`: wire Auth into the chain (Accounting → RequestID → Auth), startup warning in dev mode; compose ships example keys; README documents auth + dev mode.
- [x] 3.2 Full suite `-race` green; lint + vet clean.
- [x] 3.3 E2E with real binaries: no keys → dev-mode warning + `tenant:"dev"` on events; with keys → 401 without header (normalized, with cost event), 200 with header, tenant on the success event.
- [x] 3.4 Conventional commit(s) on `feat/tenant-auth`, push, PR based on `feat/sse-streaming`; CI green.
