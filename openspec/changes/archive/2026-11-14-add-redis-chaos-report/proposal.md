## Why

The limiter, token charge and budget read fail open when their Redis store is unreachable, and fail-closed tenants are refused with `store_unavailable`; each store call is bounded by the 250 ms limiter budget. None of this has been measured under load: how many requests a store outage admits above the configured rate, what latency it adds, which budget increments it loses, and how long the gateway takes to use the store again. The regional and provider failure reports exist, but no document puts the three failure cases side by side. The local event report counts fail-open admissions from warning lines instead of the cost event's `fail_open` field.

## What Changes

- `make chaos-redis` drives steady k6 traffic through the Compose load-test profile for a fail-open tenant (request and token limits), a budgeted tenant and a fail-closed tenant, and injects two store failures as separate arms: **hung** (`docker compose pause redis`) and **down** (`docker compose stop redis`), each for a fixed window, then restores the store.
- A verifier joins every k6 request to its cost event by request id and reports, per arm and per period (before / during / after): admitted, limited, 503 `store_unavailable`, other gateway 5xx, latency p50/p95/p99, fail-open admissions by decision, the fail-open overrun over the configured rate, budget increments lost and the store counter's agreement with them, and the time until the store answers again.
- `docs/measurements/redis-outage.md` publishes both arms; `docs/measurements/chaos-report.md` consolidates region, provider and store failures with their sources and states what was not measured.
- The event report counts fail-open admissions from the cost event's `fail_open` field (`rpm`, `tpm`, `budget`).

## Capabilities

### New Capabilities

### Modified Capabilities

- `resilience`: a store outage is measured per arm and per period, cross-checked request by request against gateway events.
- `operations`: the store-outage command restores the store and attempts verification after any failed stage; the event report reads fail-open admissions from the event field.

## Impact

`loadtest/` (new k6 client, orchestrator and verifier, fixture checks), the Compose load-test profile (three API keys and opt-in policy and price variables), the Makefile, `internal/report` and its tests, and measurement and operations documentation. Gateway request handling is unchanged.
