# Design: add-project-foundation

## Context

Empty Go repo (docs only). Constraints from [CLAUDE.md](../../../CLAUDE.md): English everything; normalized errors; readiness-based failure injection; telemetry may never block the hot path; vertical slices. Toolchain: Go 1.26, golangci-lint, Docker Compose. See proposal.md for motivation.

## Goals / Non-Goals

**Goals:**

- The permanent skeleton every later slice plugs into: pipeline, telemetry, ops behavior, local env, CI.
- Package boundaries that survive the whole design doc (no rework when limiter/cache/router arrive).

**Non-Goals:**

- OpenAI-compatible API surface (next change).
- Any Redis-consuming code, provider adapters beyond the mock binary, Terraform, or GCP deployment.

## Decisions

1. **Stdlib only — zero external Go dependencies.**
   `net/http` (Go 1.22+ method-pattern `ServeMux`), `log/slog` JSON logging, `signal.NotifyContext`, `crypto/rand` request IDs. Alternatives (chi/echo/gin, zap, uuid libs) add nothing measurable to a hot proxy path and dilute the "I understand what the framework does" claim. First dependency will arrive only when a slice genuinely needs it.

2. **Transport-agnostic pipeline in `internal/core`.**
   `Handler func(ctx, *Request) (*Response, error)`; `Stage func(Handler) Handler`; `Chain(stages...) Stage`. Decorator middleware is the idiomatic Go shape and matches the design doc's composable-stages contract. Stages see a domain envelope, not `*http.Request`, so every future stage (limiter, policy, cache, router) is unit-testable without HTTP. Alternative considered: stages over `http.Handler` — rejected because accounting, TPM reconciliation, and routing need typed domain data, not raw HTTP.

3. **`internal/core` is pure.** Depends on stdlib only; never imports other internal packages. Domain types (`Request`, `Response`, `CostEvent`, `Error`) live here; import cycles become impossible by construction.

4. **Accounting as the outermost stage, with recovery.**
   One stage owns the request's `CostEvent`: starts the clock, defers emission (executes on success, error, and panic), recovers panics into the normalized internal error. This implements the spec's "exactly one cost event per request" as a structural property rather than a convention.

5. **Async emitter: buffered channel + one drain goroutine.**
   Non-blocking send (`select`/`default`); overflow increments an atomic drop counter; `Close()` stops intake and flushes the buffer. Sink is an interface; foundation sink writes slog JSON to stdout — swapped for Pub/Sub later without touching producers. Alternative (write synchronously, "it's just a log line") rejected: the no-blocking invariant must be load-bearing from day one.

6. **Errors: `core.Error{Code, Status, Message}`.**
   Stable machine codes (`internal`, `not_found`, `invalid_request`, …), one JSON rendering in the HTTP layer, `errors.As` for mapping. Unknown errors map to `internal` without leaking details.

7. **Readiness as an atomic flag wired into shutdown order.**
   SIGTERM → flag flips (readyz 503) → configurable drain delay so LB probes observe the 503 before the listener closes → `http.Server.Shutdown` with grace timeout → emitter `Close()` (flush) → exit. This ordering is exactly the drain contract V4's chaos experiment will exercise.

8. **Config from `PUSAQ_*` env vars in `internal/config`.**
   Explicit parsing with defaults and validation errors; no config library. Cloud Run injects env vars natively; Compose sets them locally.

9. **Mock provider as a separate binary (`cmd/mockprovider`).**
   Logic in `internal/mockprovider` (testable); flags for listen address, p50/p99 latency, error rate. Returns a canned OpenAI-shaped JSON body so the next slice can proxy to it unchanged. Runs as a Compose service.

10. **Tooling choices.** Multi-stage Dockerfile (CGO_ENABLED=0 → distroless/static). Compose pins `redis:7-alpine` (Memorystore parity) with a healthcheck even though no code touches it yet — the local-env contract for the limiter slice. golangci-lint with a curated set (govet, staticcheck, errcheck, revive, unused, ineffassign, misspell, gocritic). CI: lint → vet → `go test -race -cover` → build.

## Risks / Trade-offs

- **`/v1/echo` is throwaway surface.** Accepted: the spec needs an observable request path before the real API exists; the endpoint is documented as temporary and removed by the chat-completions change.
- **Dropped telemetry is invisible in-process** (counter only, no metrics endpoint yet). Accepted: the counter is exposed in logs at shutdown and via tests; a metrics endpoint arrives with observability work.
- **Zero-dependency rule may bend soon** (e.g. an OTel SDK). Accepted: the rule is "no dependency without a slice that needs it", not "never".
- **Envelope minimalism.** `core.Request` starts with only what this slice uses (ID, arrival time, operation, payload); tenant/model/token fields arrive with their slices. Risk of churn accepted to avoid speculative design.
