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

**Foundation + chat completions in place** (changes `add-project-foundation`,
`add-chat-completions`): middleware pipeline with a guaranteed cost event per
request, async cost telemetry, liveness/readiness with graceful drain, and a
OpenAI-compatible `POST /v1/chat/completions` — buffered and SSE streaming —
proxied to the mock provider through configurable model aliases, with
API-key authentication, per-tenant cost attribution, and distributed
per-tenant rate limiting — requests (RPM) and LLM tokens (TPM) with
reservation/reconciliation — as atomic Lua token buckets in Redis, plus
circuit breakers and fallback across equivalent upstreams — and any
OpenAI-compatible model behind it, Gemini included, as pure configuration —
plus the semantic cache in shadow mode: measuring hit-rates, similarity
distributions, and candidate false positives against real traffic before a
single cached answer is ever served.

## API

`POST /v1/chat/completions` — OpenAI request subset (`model`, `messages`,
`max_tokens`, `stream`). With `"stream": true` the response is Server-Sent
Events: `chat.completion.chunk` deltas passed through from the provider,
a final usage chunk, then `data: [DONE]`. The `model` field takes an alias: `pusaq/fast`,
`pusaq/best`, or `pusaq/auto` (aliases resolve to physical models in
configuration; `pusaq/auto` becomes a real routing decision in a later
change). Responses carry `X-Pusaq-Model` (the physical model served) and
`X-Pusaq-Request-Id`. All errors share one JSON shape:
`{"error":{"code":"...","message":"..."}}` — provider failures surface as
`upstream_error` (502) without leaking upstream responses.

**Rate limiting.** With `PUSAQ_REDIS_ADDR` set, each tenant gets a token
bucket in Redis decided by one atomic Lua script shared by every replica —
read-decide-decrement as a single operation, so the limit stays exact under
concurrency (proven by test: exactly the bucket capacity admitted out of 200
concurrent requests across two instances). Denials are 429 `rate_limited`
with `retry_after_ms` and a `Retry-After` header. If Redis is unreachable
the gateway **fails open** within a 250ms budget — temporarily inexact
limits beat an outage caused by the limiter itself. Unset `PUSAQ_REDIS_ADDR`
disables limiting with a startup warning.

**Token limits (TPM).** Alongside RPM, each tenant has an LLM-token bucket.
After model resolution a request is charged its input estimate (chars/4)
plus an output reservation (`max_tokens`, or `PUSAQ_TPM_RESERVE_DEFAULT`);
when actual usage is known the difference is reconciled back atomically —
on success, on error, and at stream end. Unknown usage keeps the charge.
Every cost event records `tokens_charged` next to actual usage, so the
estimate's error is measurable from the event stream.

**Resilience.** Each upstream backend is guarded by a circuit breaker
(closed / open / half-open: opens on a >50% failure rate over a 20-sample
window with a 10-sample minimum; 10s cooldown; 3 half-open probes). On an
upstream error or an open breaker, the route's fallback backends are tried
in order — at most once each, same model contract; streams fall back only
before the first byte. Failover is observable per request: every cost event
records `served_provider` and `attempts`. Backends come from
`PUSAQ_UPSTREAMS="name=url,..."` (first = primary, rest = fallbacks).

**Semantic cache (shadow mode).** `PUSAQ_CACHE_MODE=shadow` turns on the
measurement instrument: eligible completions (single-turn) are evaluated
asynchronously — embed, nearest-neighbor within an exhaustive namespace
(tenant | served model | system prompt), record, insert — without ever
serving a cached answer and without touching request latency (bounded
workers; saturation drops are counted). Every `cache_shadow` record carries
`best_similarity` plus both response fingerprints, so precision/recall for
any τ is computable offline from the log stream alone; `PUSAQ_CACHE_TAU`
(default 0.95) only labels records. Embedders: `local` (deterministic,
$0 — measures the machinery, not paraphrase quality) or
`openai:<upstream>` + `PUSAQ_EMBED_MODEL` for real embeddings. Published
numbers must say which embedder produced them.

**Calibrating τ.** The threshold is never a judgment call: `make calibrate`
sweeps a labeled pair dataset (equivalent vs trap pairs) with the same
embedder implementations the gateway runs, writes the precision/recall
curve, and prints `τ* = min{τ : precision ≥ 0.99}` — or fails loudly when
the embedder cannot separate the traps (which the local embedder, being
lexical, often cannot: that failure is the harness working). The seed
dataset (60 pairs) proves the harness; a production τ needs the
experiments-phase corpus (≥200+200 pairs) and a real embedder.

**Authentication.** Configure tenants with `PUSAQ_API_KEYS="key:tenant,..."`;
clients send `Authorization: Bearer <key>`, and every cost event carries the
tenant. Missing/unknown keys get a normalized `unauthorized` (401). With no
keys configured the gateway runs in **dev mode** — no auth, everything
attributed to the `dev` tenant, and a warning logged at startup.
`/healthz` and `/readyz` never require credentials.

## Development

Requirements: Go 1.26+, Docker with Compose, golangci-lint.

```sh
make up      # start local environment: gateway + Redis + mock provider
make run     # run the gateway on :8080
make smoke   # POST /v1/chat/completions with pusaq/fast
make test    # go test -race -cover ./...
make lint    # golangci-lint + go vet
make down    # stop local environment
```

Every request through the pipeline emits one `cost_event` JSON line on
stdout — success, error, or panic — including requested/served model and
token usage for completions. `GET /healthz` is liveness; `GET /readyz` is
readiness and turns 503 during the drain window on SIGTERM.

Configuration (env): `PUSAQ_ADDR` (default `:8080`), `PUSAQ_DRAIN_DELAY`
(`1s`), `PUSAQ_SHUTDOWN_GRACE` (`10s`), `PUSAQ_TELEMETRY_BUFFER` (`1024`),
`PUSAQ_UPSTREAMS` (`mock=http://localhost:9090`), `PUSAQ_UPSTREAM_TIMEOUT` (`30s`),
`PUSAQ_API_KEYS` (unset → dev mode), `PUSAQ_REDIS_ADDR` (unset → limiting
disabled), `PUSAQ_RPM_LIMIT` (`60`/min per tenant), `PUSAQ_RPM_BURST`
(default = limit), `PUSAQ_TPM_LIMIT` (`100000` tokens/min), `PUSAQ_TPM_BURST`
(default = limit), `PUSAQ_TPM_RESERVE_DEFAULT` (`512`).

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

## Serving a real model (Gemini)

Google exposes Gemini on an OpenAI-compatible endpoint, so a real model is
a config entry — no code:

```sh
export PUSAQ_UPSTREAMS="gemini=https://generativelanguage.googleapis.com/v1beta/openai,mock=http://localhost:9090"
export PUSAQ_UPSTREAM_KEY_GEMINI="<your Gemini API key>"
export PUSAQ_ROUTES="pusaq/fast=gemini-2.5-flash,pusaq/best=gemini-2.5-pro,pusaq/auto=gemini-2.5-flash"
make run
```

Requests to `pusaq/fast` now hit Gemini through the whole pipeline —
limits, breakers, accounting — with the mock as the fallback backend.
Upstream keys come from `PUSAQ_UPSTREAM_KEY_<NAME>` (uppercased upstream
name) and are never logged.

## Stack

Go · Cloud Run · Memorystore (Redis, Lua) · Firestore · Vertex AI
(Gemini + Claude) · Pub/Sub → BigQuery · OpenTelemetry · Terraform · k6.
All development runs locally at $0 (Docker: Redis + mock provider); GCP is
used only for measurement runs.
