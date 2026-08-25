# Design: add-tenant-auth

## Context

The pipeline, completions (buffered + streaming), and accounting exist; nothing identifies who is calling. The next slice (distributed rate limiting) keys its buckets by tenant, so attribution must exist first. See proposal.md.

## Goals / Non-Goals

**Goals:**

- Tenant identity on every admitted request and every cost event, as a pipeline stage in the position auth will keep forever (design doc §4.2: auth resolves *who* first).
- Zero-friction local development without silent insecurity.

**Non-Goals:**

- Per-tenant policies, limits, or budgets (they consume the tenant, they don't define it); key storage beyond env config (Firestore/Secret Manager is control-plane work); key rotation; hashing keys at rest.

## Decisions

1. **Auth is a pipeline stage, inside Accounting.** Chain: Accounting → RequestID → Auth → terminal. Rejections emit cost events (a tenant hammering the gateway with bad keys is an observable fact, not a silent 401); the accounting stage needs no auth knowledge — it reads the tenant the envelope accumulated. Alternative (auth in the HTTP adapter, pre-pipeline) rejected: unaccounted rejections and a second error-rendering path.

2. **The HTTP adapter only extracts the bearer token** onto the envelope (`core.Request.APIKey`); the stage decides. Parsing transport headers is transport work; deciding who someone is is domain work. The envelope keeps the pipeline transport-agnostic.

3. **Keys in config as `key:tenant` pairs** (`PUSAQ_API_KEYS="sk-a:team-support,sk-b:team-docs"`). Duplicate keys and malformed pairs are startup errors — config bugs fail fast, not at request time. Compose ships example keys so `make up && make run` exercises real auth.

4. **Dev mode is explicit.** No keys configured → tenant `dev`, one startup warning. Refusing to serve without keys would force every local run to manage secrets; serving silently would be a footgun. The warning is the contract.

5. **`unauthorized` (401) joins the normalized codes.** Message never distinguishes missing vs unknown keys beyond what the client needs; the cost event's error code makes rejections queryable.

## Risks / Trade-offs

- **Plaintext keys in env/config.** Accepted for the lab stage and documented; the control-plane slice moves them. Keys never appear in logs or cost events — only the tenant does.
- **Dev mode could reach production config.** Mitigated by the startup warning and by Terraform owning production env later; a `PUSAQ_REQUIRE_AUTH` hard switch can arrive with deployment work if needed.
- **Constant-time comparison is skipped** (map lookup). Accepted: keys are high-entropy opaque strings, not passwords; timing side-channels on map lookups are not this threat model. Documented honestly rather than cargo-culting `subtle`.
