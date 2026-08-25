# Pusaq

> An intelligent, cost-aware AI gateway.
> **Pusaq** (Quechua: "the one who guides") guides every message to the right
> model, along the most convenient route.

Point your application's `base_url` at Pusaq and get model routing, rate
limits, fallback, and cost accounting — without changing your code.

```
Applications → Pusaq → AI models
```

For every request, Pusaq decides whether the client is allowed in, whether it
exceeded its limits, whether a previous answer can be reused, which model to
use, what to do if that model fails, how much the request cost, and what to
record about it. Models are addressed through aliases (`pusaq/fast`,
`pusaq/best`, `pusaq/auto`); the served model is reported in the
`X-Pusaq-Model` response header.

## Status

**Foundation in place** (change `add-project-foundation`): middleware
pipeline with a guaranteed cost event per request, async cost telemetry,
liveness/readiness with graceful drain, mock provider, local environment,
and CI. The OpenAI-compatible API surface is the next change proposal.

## Development

Requirements: Go 1.26+, Docker with Compose, golangci-lint.

```sh
make up      # start local environment: Redis + mock provider
make run     # run the gateway on :8080
make smoke   # POST /v1/echo through the pipeline (temporary endpoint)
make test    # go test -race -cover ./...
make lint    # golangci-lint + go vet
make down    # stop local environment
```

Every request through the pipeline emits one `cost_event` JSON line on
stdout — success, error, or panic. `GET /healthz` is liveness; `GET /readyz`
is readiness and turns 503 during the drain window on SIGTERM.
`POST /v1/echo` is a temporary smoke endpoint, removed when chat completions
lands.

Configuration (env): `PUSAQ_ADDR` (default `:8080`), `PUSAQ_DRAIN_DELAY`
(`1s`), `PUSAQ_SHUTDOWN_GRACE` (`10s`), `PUSAQ_TELEMETRY_BUFFER` (`1024`).

> Note (Fedora): if `localhost:9090`/`localhost:6379` hang while the
> containers are healthy, the firewall is blocking host→container traffic on
> the Docker bridge. The containers still reach each other inside the Compose
> network; run the mock natively with `make mock` as a workaround.

- [`docs/design.md`](docs/design.md) — full design: architecture, request
  path, theory, technology choices, phased scope (V1–V4), experiments,
  costs, risks.
- [`openspec/`](openspec/) — spec-driven workflow ([OpenSpec](https://github.com/Fission-AI/OpenSpec)):
  every change goes proposal → approved plan → vertical slice → measured
  verification → archive.
- [`CLAUDE.md`](CLAUDE.md) — the project constitution: non-negotiable design
  rules.

## Stack

Go · Cloud Run · Memorystore (Redis, Lua) · Firestore · Vertex AI
(Gemini + Claude) · Pub/Sub → BigQuery · OpenTelemetry · Terraform · k6.
All development runs locally at $0 (Docker: Redis + mock provider); GCP is
used only for measurement runs.
