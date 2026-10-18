# Pusaq

> An intelligent, cost-aware AI gateway.
> **Pusaq** (Quechua: "the one who guides") guides every message to the right
> model, along the most convenient route.

Point your application's `base_url` at Pusaq and get routing, rate limits,
fallback, and cost accounting — without changing your code.

```
Applications  →  Pusaq  →  AI models
                 (OpenAI-compatible)
```

## Why

Once an LLM is in production, three problems show up at once: one team's
runaway loop exhausts everyone's provider quota, a provider outage becomes
your outage, and nobody can say who spent what. Pusaq sits in front of the
models and answers, for every request: is this client allowed, is it within
its limits, which model should serve it, what happens if that model fails,
and what did it cost.

## Try it

```sh
make up        # gateway + Redis + a mock provider (needs Go 1.26+ and Docker)

curl -i localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"pusaq/fast","messages":[{"role":"user","content":"hello"}]}'
```

```
HTTP/1.1 200 OK
X-Pusaq-Model: mock-model            ← the physical model that served it
X-Pusaq-Route-Reason: direct
{"object":"chat.completion","choices":[{"message":{"role":"assistant", ...
```

Every request also leaves one cost event on stdout (abridged):

```json
{"tenant":"dev","outcome":"success","requested_model":"pusaq/fast","served_model":"mock-model",
 "served_provider":"mock","attempts":1,"prompt_tokens":12,"completion_tokens":11,"cost_usd":0.0000056}
```

Apps address models through aliases (`pusaq/fast`, `pusaq/best`, `pusaq/auto`);
the physical names live in configuration, never in code. Upstreams are
OpenAI-compatible servers named in configuration — see [upstreams](docs/gateway.md#upstreams).

## What it does

| | |
|---|---|
| **OpenAI-compatible API** | Chat completions with SSE streaming and normalized errors; nothing beyond that subset. [Details](docs/gateway.md#api) |
| **Tenants and policies** | API-key tenants; per-tenant allowed models, prompt cap, monthly dollar budget, fail-closed mode. [Details](docs/gateway.md#tenant-policies) |
| **Rate limiting** | Requests and LLM tokens per minute as atomic Lua token buckets in Redis; strict or lease mode. [Details](docs/gateway.md#rate-limiting) |
| **Resilience** | Circuit breaker per backend and ordered fallback; streams fall back only before the first byte. [Details](docs/gateway.md#resilience) |
| **Content rules** | Flags personal data, secrets, prompt-injection phrasing and time-dependent questions; tenants may block. A policy filter, not a firewall. [Details](docs/gateway.md#content-rules) |
| **Semantic cache** | Runs in shadow mode: measures would-be hits and false positives, serves nothing. [Details](docs/gateway.md#semantic-cache-shadow-mode) |
| **Cost telemetry** | One asynchronous cost event per request admitted to the pipeline — success, error, panic or stream end — priced against a versioned table. |

Configuration is environment variables only: [docs/configuration.md](docs/configuration.md).

## Measured, not claimed

Every guarantee below was measured under load or injected failure by one
reproducible command, and is published with its protocol.

| Property | Result | Report |
|---|---|---|
| Request limit across 2 replicas at 10× overload | 1299 and 3099 requests admitted, inside the exact bounds; limiter adds < 0.5 ms | [limiter](docs/measurements/limiter-exactness.md) |
| Token limit overrun with requests in flight | Peak excess 445 tokens against a stated ceiling of 1300; never over the window bound | [tpm](docs/measurements/tpm-overrun.md) |
| Circuit breaker | Opens 0.83–0.92 s after the primary dies; closes ≈ 6 s after it returns; 0 client errors | [breaker](docs/measurements/breaker-detection.md) |
| Regional failover on GCP (2 regions, global load balancer) | New requests reach the other region 4.1–5.6 s after readiness fails; one retry recovers 41 of 45 failures; 0 streams interrupted | [failover](docs/measurements/regional-failover.md) |
| Content rules on a labeled EN/ES corpus | Sensitive and injection 1.00/1.00; temporal 0.97 precision, 0.89 recall | [content rules](docs/measurements/content-rules.md) |

## Honest limits

- The semantic cache never serves an answer: it runs in shadow mode,
  recording would-be hits and their false positives.
- With one Redis per region, limits are per region during a failover, not
  globally exact; the measured overrun is in the failover report.
- A readiness failure is a soft failure; a region dying abruptly can cut
  streams in flight, and that case was not measured.
- Content rules are heuristics with published error rates, and the two-region
  GCP deployment exists only for measurement runs.

## Development

```sh
make test      # go test -race -cover ./...
make lint      # golangci-lint + go vet + lint-docs
make help      # every target: load tests, calibration, Terraform
```

- [docs/gateway.md](docs/gateway.md) — how each mechanism works
- [docs/configuration.md](docs/configuration.md) — every `PUSAQ_*` variable
- [docs/operations.md](docs/operations.md) — telemetry, load tests, cloud deployment
- [docs/design.md](docs/design.md) — architecture, trade-offs, measurement protocol
- [`openspec/`](openspec/) — spec-driven workflow: every change goes proposal → plan → vertical slice → measured verification → archive
- [`CLAUDE.md`](CLAUDE.md) — the project constitution: non-negotiable design rules
