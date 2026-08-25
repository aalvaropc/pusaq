# Tasks: add-project-foundation

## 1. Module and core domain

- [x] 1.1 Initialize `go.mod` (module `github.com/aalvaropc/pusaq`, go 1.26); verify `go build ./...` succeeds on the empty module.
- [x] 1.2 `internal/core`: `Request`/`Response` envelope, `CostEvent`, normalized `Error` type with stable codes and `errors.As` support; verify with table-driven tests (`go test ./internal/core`).
- [x] 1.3 `internal/core/pipeline.go`: `Handler`, `Stage`, `Chain`; tests prove composition order and short-circuiting.

## 2. Telemetry

- [x] 2.1 `internal/telemetry`: `Sink` interface, slog-JSON stdout sink, async `Emitter` (buffered channel, non-blocking send, atomic drop counter, `Close()` flush); tests prove: slow sink never blocks producers, full buffer increments drop counter, `Close()` flushes buffered events. Run with `-race`.

## 3. Config

- [x] 3.1 `internal/config`: parse `PUSAQ_ADDR`, `PUSAQ_SHUTDOWN_GRACE`, `PUSAQ_TELEMETRY_BUFFER` (defaults + validation errors); table-driven tests for defaults, overrides, and invalid values.

## 4. Gateway

- [x] 4.1 `internal/gateway/stages.go`: request-ID stage (crypto/rand, response header, cost event) and recovery+accounting stage (defer-emitted `CostEvent`, panic → normalized internal error); tests prove exactly one cost event on success, handler error, and panic.
- [x] 4.2 `internal/gateway/server.go`: ServeMux with `GET /healthz`, `GET /readyz` (atomic readiness flag), `POST /v1/echo` through the pipeline; normalized JSON error rendering; httptest coverage for all three routes and the error shape.
- [x] 4.3 Graceful drain in `cmd/pusaq/main.go` + gateway: `signal.NotifyContext` → readiness 503 → `Server.Shutdown` with grace → emitter flush → exit; verify with an httptest-level drain test (in-flight request completes while readyz is 503) and a manual SIGTERM check.

## 5. Mock provider

- [x] 5.1 `internal/mockprovider` + `cmd/mockprovider`: HTTP server returning a canned OpenAI-shaped chat-completion JSON, flags `--addr`, `--latency-p50`, `--latency-p99`, `--error-rate`; tests cover latency sampling bounds and error-rate behavior.

## 6. Local environment and CI

- [x] 6.1 Multi-stage `Dockerfile` (CGO_ENABLED=0, distroless/static) building both binaries; verify `docker build .` succeeds.
- [x] 6.2 `docker-compose.yml`: `redis:7-alpine` with healthcheck + `mockprovider` built from the repo; verify `docker compose up -d` reports both healthy/running.
- [x] 6.3 `Makefile` (`up`, `down`, `run`, `mock`, `test`, `lint`, `tidy`, `smoke`) and `.editorconfig`; verify each target runs.
- [x] 6.4 `.golangci.yml` (govet, staticcheck, errcheck, revive, unused, ineffassign, misspell, gocritic); verify `make lint` is clean.
- [x] 6.5 `.github/workflows/ci.yml`: golangci-lint, `go vet`, `go test -race -cover`, `go build ./...`; verify the workflow is green on the PR.

## 7. Docs and delivery

- [x] 7.1 README: Development/Quickstart section (`make up`, `make run`, `make smoke`), note that `/v1/echo` is temporary; verify quickstart commands work as written.
- [x] 7.2 End-to-end smoke: `make up` + `make run`, `curl /healthz` 200, `curl /readyz` 200, `make smoke` returns the echo and exactly one cost-event JSON line appears on stdout.
- [x] 7.3 Conventional commit(s) on `feat/project-foundation`, push, open PR to `main` (no co-authorship); verify PR opens with CI running.
