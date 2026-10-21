# Pusaq

A Go gateway for OpenAI-compatible chat completions. Put it between your app
and model providers to control access, quotas, routing and cost.

```text
Your app → Pusaq → Model providers
```

## Capabilities

- Configurable model aliases, routing rules, circuit breakers and fallback.
- Tenant API keys, model permissions and monthly spending limits.
- Request and token quotas enforced through atomic Redis Lua scripts.
- Buffered and SSE responses, token usage and cost events.
- Content rules for sensitive data, injection phrases and temporal queries.

## Run locally

Requires Docker Compose and Make. The default stack starts the gateway,
Redis and mock providers; no provider API key is needed.

```sh
git clone https://github.com/aalvaropc/pusaq.git
cd pusaq
make up

curl http://localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"pusaq/fast","messages":[{"role":"user","content":"hello"}]}'
```

Use `http://localhost:8080/v1` as your client's base URL and a configured
alias such as `pusaq/fast` as the model. Run `make down` to stop the stack.

## Limits

The API supports chat completions and SSE; tool calling and structured
outputs are not implemented. The semantic cache runs in shadow mode and
never serves responses. Content rules are heuristics. Quotas are regional
during failover, and abrupt regional failures can interrupt streams.

## Development and docs

Go 1.26+ is required for local development. Run `make test` for tests with
the race detector and `make lint` for static checks.

- [Gateway](docs/gateway.md) · [Configuration](docs/configuration.md)
- [Architecture](docs/design.md) · [Operations](docs/operations.md)
- [Measurement reports](docs/measurements/): results, commands and limitations

## License

[MIT](LICENSE)
