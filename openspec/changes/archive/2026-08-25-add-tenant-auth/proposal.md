# Proposal: add-tenant-auth

## Why

Everything the gateway will govern — rate limits, budgets, policies, cost reports — is *per tenant*, and no tenant exists yet. Attribution must land before the rate-limiting change: a limiter without tenant identity has nothing to key its buckets on.

## What Changes

- API-key authentication as a pipeline stage: `Authorization: Bearer <key>` resolves the tenant; missing or unknown keys are rejected with a new normalized `unauthorized` code (401) **before any provider is called**.
- Tenants and keys come from configuration (`PUSAQ_API_KEYS`, `key:tenant` pairs). With no keys configured the gateway runs in **dev mode**: every request is attributed to the `dev` tenant and a warning is logged at startup — local development stays friction-free, and the behavior is explicit, never silent.
- The request envelope and cost events gain the tenant; every cost event — including 401 rejections — is attributable.
- Operational endpoints (`/healthz`, `/readyz`) remain unauthenticated.

## Capabilities

### New Capabilities

- `tenant-auth`: API-key authentication and tenant attribution of every gateway request.

### Modified Capabilities

- `cost-telemetry`: cost events carry the tenant.

## Impact

- Modified: `internal/core` (envelope `APIKey`/`Tenant`, event tenant, `unauthorized` code), `internal/gateway` (auth stage, adapter passes the bearer token), `internal/config` (key parsing/validation), `internal/telemetry` (tenant attribute), `cmd/pusaq` (wiring + dev-mode warning), README, docker-compose (example keys).
- No new external Go dependencies. Real secret management (Firestore/Secret Manager) is a later control-plane change.
