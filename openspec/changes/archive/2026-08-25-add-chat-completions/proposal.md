# Proposal: add-chat-completions

## Why

The foundation proves the pipeline with a throwaway echo endpoint; Pusaq's actual contract is "point your OpenAI SDK at a new base_url and it works". This change delivers the first real piece of that contract — non-streaming chat completions proxied to a provider through the pipeline — and establishes the provider abstraction and model-alias resolution.

## What Changes

- `POST /v1/chat/completions` (non-streaming): OpenAI-compatible request/response subset, proxied to a provider through the existing pipeline.
- Model aliases `pusaq/fast`, `pusaq/best`, `pusaq/auto` resolved from configuration to a provider + upstream model; physical model names never appear in code (constitution rule 1). Unknown models are rejected with a normalized error.
- `X-Pusaq-Model` response header reporting the served model (constitution rule 7).
- Provider abstraction (`internal/provider`): a small `Provider` interface plus an HTTP adapter for the mock provider; upstream failures map to a new normalized `upstream_error` code (HTTP 502), timeouts bounded by config.
- Cost events gain model fields (requested alias, served model) and token usage passthrough.
- **BREAKING (dev-only surface):** `POST /v1/echo` is removed, as the foundation change promised.
- README: API section replaces the echo quickstart.

## Capabilities

### New Capabilities

- `chat-completions`: the OpenAI-compatible chat-completions subset — request validation, alias resolution, provider proxying, served-model reporting, and error mapping.

### Modified Capabilities

- `cost-telemetry`: cost events for completion requests additionally carry requested/served model and token usage.

## Impact

- New code: `internal/provider` (interface + mock HTTP adapter), completions types and terminal handler in `internal/gateway`, alias/routing table and upstream settings in `internal/config`.
- Modified: `internal/core` (CostEvent model/usage fields, `upstream_error` code), `internal/gateway` (route swap: echo → chat completions), `cmd/pusaq` (wiring), README, Makefile smoke target.
- No new external Go dependencies. The mock provider remains the only upstream.
