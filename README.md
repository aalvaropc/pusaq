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

**Design phase.** No code yet — deliberately: the Go skeleton is the first
spec-driven change proposal.

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
