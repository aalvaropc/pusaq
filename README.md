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

What the gateway does today:

- **OpenAI-compatible chat completions** — `POST /v1/chat/completions`,
  buffered and SSE streaming, model aliases resolved from configuration,
  `X-Pusaq-Model` on every response, normalized errors.
- **Tenants** — API-key authentication with per-tenant cost attribution;
  explicit dev mode when no keys are configured.
- **Distributed rate limiting** — per-tenant requests (RPM) and LLM tokens
  (TPM) as atomic Lua token buckets in Redis; TPM charges a reservation on
  admission and reconciles it exactly once when actual usage is known.
- **Resilience** — a circuit breaker per upstream backend and ordered
  fallback across equivalent backends; streams fall back only before the
  first byte.
- **Any OpenAI-compatible upstream** — Gemini included — as pure
  configuration, with per-upstream credentials.
- **Semantic cache in shadow mode** — measures would-be hits, similarity
  distributions, and candidate false positives against real traffic; serves
  nothing. A calibration command picks the similarity threshold for a
  precision floor.
- **Cost telemetry** — exactly one asynchronous cost event per request, on
  success, error, panic, and stream end alike; completions are priced in
  dollars against a versioned price table from configuration.

Not implemented: serving from the cache, a policy filter, and multi-region
deployment.

## API

`POST /v1/chat/completions` — OpenAI request subset (`model`, `messages`,
`max_tokens`, `stream`). With `"stream": true` the response is Server-Sent
Events: `chat.completion.chunk` deltas passed through from the provider,
a final usage chunk, then `data: [DONE]`. The `model` field takes an alias:
`pusaq/fast` and `pusaq/best` resolve to physical models in configuration;
`pusaq/auto` resolves through routing rules keyed by the request's
`metadata.feature_tag` (`PUSAQ_ROUTING_RULES`, default
`classify`/`extract` → fast, `reason`/`code` → best, everything else →
fast). Responses carry `X-Pusaq-Model` (the physical model served),
`X-Pusaq-Route-Reason` (`direct`, `rule:<tag>`, `default:untagged`, or
`default:unknown_tag`) and `X-Pusaq-Request-Id`; the cost event records the
same as `route_reason` and `feature_tag`. All errors share one JSON shape:
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

Two modes, chosen with `PUSAQ_RATELIMIT_MODE`. **`strict`** (default): one
atomic store decision per request, exact across replicas. **`lease`**: each
replica takes up to `PUSAQ_LEASE_SIZE` request tokens per round-trip and
admits locally; unused tokens return when the lease passes `PUSAQ_LEASE_TTL`
or the replica drains, and a store denial is cached locally until its retry
time. Cumulative admissions never exceed the bucket in either mode; in lease
mode up to `replicas × lease size` tokens can sit unspent in leases at any
moment, and a replica that dies without draining strands at most one lease
per tenant (under-admission, never overrun). Lease mode covers request
limits only; token limits are always strict. Both modes are measured with
the same load test.

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
dataset (60 pairs) proves the harness; a production τ needs a
production-traffic corpus (≥200+200 pairs) and a real embedder.

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
`PUSAQ_ROUTES` (`alias=model,...` for `pusaq/fast` and `pusaq/best`),
`PUSAQ_ROUTING_RULES` (`tag=alias,...,default=alias`; replaces the default
table), `PUSAQ_API_KEYS` (unset → dev mode), `PUSAQ_REDIS_ADDR` (unset → limiting
disabled), `PUSAQ_RPM_LIMIT` (`60`/min per tenant), `PUSAQ_RPM_BURST`
(default = limit), `PUSAQ_RATELIMIT_MODE` (`strict` | `lease`),
`PUSAQ_LEASE_SIZE` (`10`, at most the burst), `PUSAQ_LEASE_TTL` (`1s`),
`PUSAQ_TPM_LIMIT` (`100000` tokens/min; `0` disables token
limiting with a startup warning), `PUSAQ_TPM_BURST` (default = limit),
`PUSAQ_TPM_RESERVE_DEFAULT` (`512`), `PUSAQ_PRICES` (unset → no cost
fields; `model=usd_per_1M_input/usd_per_1M_output,...` keyed by physical
model, every route model must be priced), `PUSAQ_PRICE_TABLE_VERSION`
(required with `PUSAQ_PRICES`; travels on every priced event as
`price_table_version` next to `cost_usd`).

> Note (Fedora): if `localhost:9090`/`localhost:6379` hang while the
> containers are healthy, the firewall is blocking host→container traffic on
> the Docker bridge. The containers still reach each other inside the Compose
> network; run the mock natively with `make mock` as a workaround.

- [`docs/design.md`](docs/design.md) — architecture, request path,
  theory, technology choices, design constraints, measurement protocol,
  risks.
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
export PUSAQ_ROUTES="pusaq/fast=gemini-2.5-flash,pusaq/best=gemini-2.5-pro"
export PUSAQ_ROUTING_RULES="classify=pusaq/fast,extract=pusaq/fast,reason=pusaq/best,code=pusaq/best,default=pusaq/fast"
make run
```

Requests to `pusaq/fast` now hit Gemini through the whole pipeline —
limits, breakers, accounting — with the mock as the fallback backend.
Upstream keys come from `PUSAQ_UPSTREAM_KEY_<NAME>` (uppercased upstream
name) and are never logged.

## Measurements

Every property the gateway claims is measured under load or under injected
failure, by one reproducible command, and published with its protocol in
[`docs/measurements/`](docs/measurements/):

- [`limiter-exactness.md`](docs/measurements/limiter-exactness.md) — the
  request limit across two gateway replicas under 10× overload, co-tenant
  isolation, and the limiter's latency toll. `make loadtest` runs it
  (Compose profile `loadtest`: two limited and two unlimited replicas behind
  nginx, a zero-latency mock, k6 in a container); `make loadtest-down` stops
  it. Results are cross-checked from k6, the mock provider's `GET /stats`
  counter, and the gateways' cost events.

## Cloud

The cloud side is declared as code in [`infra/terraform/foundation/`](infra/terraform/foundation/)
and creates only what publishing needs — nothing that runs continuously:

- an Artifact Registry repository for the gateway and mock-provider images,
  with cleanup policies (untagged layers after 7 days, tagged images after
  30 days except the 10 most recent);
- a publisher service account reachable only through workload identity
  federation from this repository's GitHub Actions, with write access to
  that one registry and no project-level roles;
- a monthly budget over every resource labeled `project=pusaq`, alerting an
  existing notification channel at 50/75/90/100 %.

```sh
cp infra/terraform/foundation/terraform.tfvars.example infra/terraform/foundation/terraform.tfvars
make infra-init      # state lives in an existing bucket, passed at init time
make infra-plan      # read it: registry, identity, budget — nothing else
make infra-apply
```

After the first apply, set the two repository variables the publishing
workflow reads (`GCP_WORKLOAD_IDENTITY_PROVIDER`, `GCP_SERVICE_ACCOUNT`) from
the Terraform outputs. From then on every push to `main` publishes
`pusaq` and `mockprovider` images tagged with the commit SHA and `main`
([`release-image.yml`](.github/workflows/release-image.yml)). CI checks
Terraform formatting and validity without credentials.

## Stack

Go · Cloud Run · Memorystore (Redis, Lua) · Firestore · Vertex AI
(Gemini + Claude) · Pub/Sub → BigQuery · OpenTelemetry · Terraform · k6.
All development runs locally (Docker: Redis + mock provider); cloud
resources exist only for measurement runs.
