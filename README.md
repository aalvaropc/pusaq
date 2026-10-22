# Pusaq

[![CI](https://github.com/aalvaropc/pusaq/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/aalvaropc/pusaq/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/aalvaropc/pusaq)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**One gateway for model routing, tenant quotas and AI cost accounting.**

Point your app at Pusaq's OpenAI-compatible chat endpoint. Choose a model
alias; Pusaq handles access, limits and provider fallback.

[Documentation](docs/README.md) · [Configuration](docs/configuration.md) · [Security](SECURITY.md)

```text
Your app → Pusaq → Primary provider
                ↘ Fallback providers
```

## What you get

| Capability | Included |
|---|---|
| Routing | Configurable aliases, feature rules, circuit breakers and fallback |
| Tenant controls | API keys, model permissions and monthly spending limits |
| Quotas | Request and token buckets with atomic Redis operations |
| Visibility | Buffered and SSE responses, token usage and JSON cost events |
| Policy filter | Content heuristics and a semantic cache in shadow mode |

## Quick start

Requires Docker Compose, Make and curl. Starts Redis and mock providers with no
provider API key or cloud account.

```sh
git clone https://github.com/aalvaropc/pusaq.git
cd pusaq
make up
curl --fail --retry 10 --retry-connrefused --retry-delay 1 http://localhost:8080/readyz

curl --fail-with-body http://localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"pusaq/fast","messages":[{"role":"user","content":"hello"}]}'
```

Client base URL: `http://localhost:8080/v1`. Model: `pusaq/fast`,
`pusaq/best` or `pusaq/auto`. Stop with `make down`.

The local stack explicitly enables **unauthenticated development mode** and
binds ports to loopback. Outside that mode, API keys are required. See
[setup and authentication](docs/operations.md#local-environment).

## Scope

Supports a [chat-completions subset](docs/gateway.md#api); tool calling and
structured outputs are not implemented. The cache never serves responses.
Quotas are regional during failover; abrupt failures can interrupt streams.

## Development

Requires **Go 1.26.10+**. Run `make test` for race tests and `make lint` for
static checks. See [operations](docs/operations.md) and
[measured results](docs/measurements/README.md) for commands and limitations.

## License

[MIT](LICENSE)
