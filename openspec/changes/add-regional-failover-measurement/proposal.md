# Proposal: add-regional-failover-measurement

## Why

The design promises that when one of Pusaq's regions stops being ready, the global balancer sends new requests to the other region, and that what happens to in-flight requests, streams, and split quotas is counted rather than estimated (docs/design.md §5.6, §7, §8). Nothing measures it: there is no two-region deployment, no way to fail a region's readiness on purpose, and the events cannot be read back per region or per run. This change builds the deployment as code, the injection knob, the harness, and the run.

## What Changes

- **Gateway:** an admin endpoint, served only when `PUSAQ_ADMIN_TOKEN` is set, opens a not-ready window for a duration: `/readyz` answers 503 and new requests are refused with 503 `not_ready` and a `Retry-After`, while in-flight requests and streams finish. Draining on SIGTERM is unchanged. Every response carries `X-Pusaq-Region` and every cost event a `region` field when `PUSAQ_REGION` is set; `metadata.run_id` travels into the event as `run_id`.
- **Mock provider:** `--stream-chunk-delay`, so a streamed completion can last long enough to be in flight when readiness is failed.
- **Runtime module** (`infra/terraform/runtime/`): the gateway in two Cloud Run regions with a readiness probe, one Memorystore instance per region, the mock provider per region on internal ingress, and a global external Application Load Balancer over serverless NEGs that follows Cloud Run service health. Created for a run and destroyed the same day (`make runtime-*`).
- **Harness:** `make chaos-run` drives `loadtest/chaos.js` against the balancer — a retried non-streaming arm, a streaming arm, a split-quota tenant, per-region readiness probes, and the flip at a known instant — then `fetch-events.sh` collects the gateways' events from Cloud Logging and `verify-chaos.sh` computes time to the other region, the longest interval without a success, failures / recoveries by retry / failures after retry, streams complete / interrupted / refused, latency before / during / after per region, and the split tenant's admissions per region, with integrity checks. `make chaos-local` runs the same harness on the Compose profile at no cost.
- **Docs:** `docs/measurements/regional-failover.md` publishes the protocol, environment, results, and caveats; README and `docs/design.md` §8 describe the run.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `operations`: the readiness flag can be forced not-ready for a window through a token-protected admin endpoint; new requests are refused while in-flight ones finish.
- `chat-completions`: responses identify the serving region; requests may declare a run id; a refused request is 503 `not_ready`.
- `cost-telemetry`: events carry the region and the run id.
- `infrastructure`: a runtime module deploys the gateway for measurement runs only.
- `resilience`: regional failover under load is measured by one reproducible command.

## Impact

- New: `infra/terraform/runtime/*`, `loadtest/chaos.js`, `loadtest/chaos-run.sh`, `loadtest/fetch-events.sh`, `loadtest/verify-chaos.sh`, `docs/measurements/regional-failover.md`.
- Modified: `internal/gateway/server.go` (admin endpoint, region header, injected window), `internal/gateway/completions.go` (run id), `internal/core` (error code, event fields), `internal/config` (two variables), `internal/telemetry`, `internal/mockprovider` and `cmd/mockprovider` (chunk delay), `cmd/pusaq/main.go`, `Makefile`, `docker-compose.yml`, `.github/workflows/ci.yml`, README, `docs/design.md`.
- No new Go dependencies. Terraform gains the `random` provider for the admin token.
