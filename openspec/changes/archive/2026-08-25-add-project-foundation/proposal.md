# Proposal: add-project-foundation

## Why

Pusaq has a full design ([docs/design.md](../../../docs/design.md)) but no code, and the constitution requires every change to end in something observable and verified. Before any feature (rate limiting, caching, routing) can be built, the project needs its structural invariants in place: a composable request pipeline, cost telemetry that can never block or be skipped, operational endpoints that support draining, and a reproducible local environment.

## What Changes

- Go module `github.com/aalvaropc/pusaq` with a stdlib-only foundation (no external Go dependencies).
- Transport-agnostic middleware pipeline (`Handler` / `Stage` / `Chain`) with two permanent stages: request-ID assignment and recovery + accounting.
- Asynchronous cost-event emitter: buffered, non-blocking, with a drop counter — the response path never waits on telemetry.
- Normalized error model (code, HTTP status, message) rendered as a single JSON error shape.
- Operational endpoints: `GET /healthz` (liveness), `GET /readyz` (readiness backed by an atomic flag), SIGTERM draining (not-ready → in-flight grace → shutdown).
- Temporary smoke endpoint `POST /v1/echo` routed through the pipeline; removed when the chat-completions capability lands.
- Mock provider binary (canned OpenAI-shaped response; configurable latency and error rate) for future $0 experiments.
- Local environment: Docker Compose (Redis 7 + mock provider), multi-stage Dockerfile, Makefile, golangci-lint config, GitHub Actions CI (lint, vet, race tests, build).

## Capabilities

### New Capabilities

- `request-pipeline`: composable stage pipeline every gateway request traverses; guarantees exactly one cost event per request regardless of outcome (success, error, panic).
- `cost-telemetry`: asynchronous cost-event emission that never blocks the response path; overflow drops events and counts the drops.
- `operations`: liveness, readiness, and graceful drain behavior of the gateway process.

### Modified Capabilities

None — this is the first change; no specs exist yet.

## Impact

- New code: `cmd/pusaq`, `cmd/mockprovider`, `internal/core`, `internal/gateway`, `internal/telemetry`, `internal/config`, `internal/mockprovider`.
- New tooling: `Makefile`, `Dockerfile`, `docker-compose.yml`, `.golangci.yml`, `.github/workflows/ci.yml`, `.editorconfig`.
- Docs: README gains a Development/Quickstart section.
- No external Go dependencies introduced; Redis appears in Compose only (no code uses it yet — it anchors the local environment contract for the rate-limiting change).
