# Documentation

Pusaq is a Go gateway for an OpenAI-compatible chat-completions subset.
Configuration is validated at startup; Redis holds quotas and monthly spend.
The gateway emits JSON accounting events and supports provider fallback.

## Start here

| Goal | Read |
|---|---|
| Try the gateway with local mocks | [Quick start](../README.md#quick-start) |
| Run locally, authenticate clients or inspect logs | [Operations](operations.md) |
| Understand accepted requests, SSE and errors | [Gateway reference](gateway.md) |
| Configure tenants, providers, quotas or shadow sampling | [Configuration](configuration.md) |
| Understand the request pipeline and design choices | [Architecture](design.md) |
| Inspect reproducible tests and dated experiment results | [Measurements](measurements/README.md) |
| Report a vulnerability privately | [Security policy](../SECURITY.md) |

## Operating boundaries

- The default Compose stack uses explicit unauthenticated development mode
  and publishes ports on loopback. The standalone gateway requires API keys
  unless development mode is explicitly enabled.
- Provider credentials require HTTPS, with a development exception for
  localhost and literal loopback addresses. Provider and embedding clients
  do not follow redirects.
- The semantic cache is off by default in the standalone binary; Compose
  enables shadow sampling. It never serves responses. The index retains
  vectors and fingerprints; queued evaluations temporarily retain eligible
  prompts under a separate data budget.
- Content rules are heuristics. Tool calling, structured outputs and
  response-secret scanning are not implemented.
- Quotas are regional when regions use separate Redis instances. Hard
  regional failures can interrupt streams. The Terraform runtime is a
  temporary measurement environment, not a production deployment template.

The references describe the implementation. Measurement reports identify
their dates and environments; their results are not deployment guarantees.
