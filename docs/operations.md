# Operations

Run locally, inspect telemetry, verify changes and operate temporary
measurement environments. Start with the [documentation index](README.md)
or consult [configuration](configuration.md) for all settings.

## Telemetry and health

Each completion request entering the accounting pipeline produces one
`cost_event`, finalized on success, error, panic or stream end. Missing or
unknown API keys are accounted before body reads and have no payload-derived
run ID. Authenticated body failures and malformed run IDs are accounted as
`invalid_request`; valid run IDs survive RPM rejection. Injected not-ready
refusals happen before accounting and produce no cost event or request ID.

Events are offered to a bounded asynchronous emitter that writes JSON lines
to stdout. Saturation drops events rather than waiting for the sink;
`telemetry_dropped` reports the count at shutdown. A produced event therefore
does not guarantee a persisted log record. Completion events include resolved
model and usage when available; configured prices add `cost_usd`,
`price_table_version` and, for estimated costs, `cost_estimated`.

Prompt text, raw provider bodies, URL-bearing transport errors and recovered
panic values are omitted from gateway diagnostics. Policy events carry rule
kinds rather than matched content. Treat metadata and retained logs as
sensitive. External embedding services receive eligible shadow prompts.

`GET /healthz` is liveness; `GET /readyz` is readiness. Neither requires
credentials. Readiness turns 503 during the drain window on SIGTERM. With
`PUSAQ_ADMIN_TOKEN` set, `POST /admin/readiness?state=down&for=<duration>`
(bearer token) fails readiness on purpose for that long — `/readyz` answers
503 and new requests are refused with `not_ready` while in-flight requests
and streams finish; `state=up` ends it early. It is the failure-injection
knob chaos runs use instead of deleting a service; each action is one
`readiness_window` log record.

Shutdown marks the replica not ready, serves during `PUSAQ_DRAIN_DELAY`,
then gives in-flight requests `PUSAQ_SHUTDOWN_GRACE` to finish. Lease tokens
are returned, shadow evaluations drained and telemetry flushed. During a
readiness injection, in-flight work continues; hard process or regional
failure can interrupt it.

## Local environment

### Docker Compose

Requires Docker with Compose, Make and curl. Go is supplied by the image
build. The default stack starts the gateway, Redis and two mocks:

```sh
make up
curl --fail --retry 10 --retry-connrefused --retry-delay 1 http://localhost:8080/readyz
make smoke
```

This service explicitly sets `PUSAQ_DEV_MODE=true`. Requests are
unauthenticated and attributed to `dev`. It also enables shadow sampling
with the local embedder and synthetic mock prices. Stop with `make down`.

The gateway (8080), Redis (6379) and mock (9090) are published only on
`127.0.0.1`. Containers reach each other by service name. Access from another
machine requires an explicit Compose override of the service port bindings;
the default stack does not expose these services to the surrounding network.

### Gateway from source

Requires Go 1.26.10+ (see [go.mod](../go.mod)). Start only the dependencies
so the containerized gateway does not compete for port 8080:

```sh
docker compose up -d --build redis mockprovider mockbackup
PUSAQ_ADDR=127.0.0.1:8080 PUSAQ_REDIS_ADDR=127.0.0.1:6379 make run
```

`make run` explicitly enables unauthenticated development mode. Run
`make smoke` in another terminal, then stop the process with Ctrl+C and the
containers with `make down`. If the complete stack is already running, stop
its gateway with `docker compose stop pusaq` before starting the host process.

### Authenticated local requests

Use the same dependencies and run the gateway directly, with development
mode disabled. Set `PUSAQ_CLIENT_KEY` to a local client key in the gateway
and client terminals. The required-value expression below refuses an unset
or empty key:

```sh
PUSAQ_DEV_MODE=false \
PUSAQ_API_KEYS="${PUSAQ_CLIENT_KEY:?set PUSAQ_CLIENT_KEY}:local" \
PUSAQ_ADDR=127.0.0.1:8080 \
PUSAQ_REDIS_ADDR=127.0.0.1:6379 \
go run ./cmd/pusaq
```

In the client terminal:

```sh
curl --fail-with-body http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer ${PUSAQ_CLIENT_KEY:?set PUSAQ_CLIENT_KEY}" \
  -H 'Content-Type: application/json' \
  -d '{"model":"pusaq/fast","messages":[{"role":"user","content":"hello"}]}'
```

This example uses a keyless local mock upstream. For real providers,
configure aliases, HTTPS upstream bases and separate provider credentials
as described in [configuration](configuration.md#upstreams-and-routing).

### Development checks

Use Go 1.26.10+ and golangci-lint 2.12.x. The shell measurement fixtures also
require Bash, jq, bc, awk and GNU date. These checks do not require a cloud
account:

```sh
make test
make lint
make test-loadtest
go build ./...
```

`make test` includes the race detector and coverage. `make lint` runs
golangci-lint, go vet and the repository documentation guard.

### Container boundaries

Image builds admit only Dockerfile, the Go module manifests, `cmd/` and
`internal/` as context. Other checkout files, including Terraform variables,
environment files, Git metadata and measurement output, are excluded.

The mock provider's admin routes are unauthenticated: `POST /admin/fail?for=5s` fails every completion for 5 s, `DELETE /admin/fail` clears it, `GET /stats` counts what reached it.

## Measurements and how to run them

The [measurement index](measurements/README.md) identifies all nine reports,
their dates and the commands that exercise each mechanism. Run load tests
against the local mock stack; their Make targets flush the stack's Redis.
Commands and reports include:

- [`limiter-exactness.md`](measurements/limiter-exactness.md) — the
  request limit across two gateway replicas under 10× overload, co-tenant
  isolation, and the limiter's latency toll. `make loadtest` runs it
  (Compose profile `loadtest`: two limited and two unlimited replicas behind
  nginx, a zero-latency mock, k6 in a container); `make loadtest-down` stops
  it. Results are cross-checked from k6, the mock provider's `GET /stats`
  counter, and the gateways' cost events.
- [`breaker-detection.md`](measurements/breaker-detection.md) — how
  fast each replica's breaker opens when the primary upstream dies at a
  known instant, how long it stays open, and how fast it closes once the
  upstream is back, with zero client errors thanks to the fallback.
  `make breakertest` runs it (same profile plus a fallback mock; the
  primary is failed on command with `POST /admin/fail?for=<duration>`).
- [`tpm-overrun.md`](measurements/tpm-overrun.md) — with token
  limiting on and an upstream that takes seconds to answer, how far the
  tokens actually consumed can exceed the bucket before reconciliation
  catches up, for an under-reserving and an over-reserving tenant.
  `make tpmtest` runs it (same profile with token limiting on and emulated
  upstream latency).
- [`regional-failover.md`](measurements/regional-failover.md) — the
  gateway in two Cloud Run regions behind a global load balancer, region
  A's readiness failed on purpose under load: new requests reach the other
  region 4.1–5.6 s after the flip; first-attempt failures are fast
  `not_ready` refusals from A's gateway or ~10 s platform 429s, and one
  retry recovers 41 of 45; zero streams interrupted (a soft failure: the
  instance keeps running); the split tenant's quota overruns its one-region
  share by ~5 %. `make chaos-run` runs it against the runtime module;
  `make chaos-local` checks the harness on the Compose profile.
- [`content-rules.md`](measurements/content-rules.md) — precision and
  recall of the policy filter's content rules (sensitive data, injection
  phrasing, temporal cues) on a 204-row labeled EN/ES corpus with hard
  negatives, against floors the test suite enforces
  (`go test -v -run TestContentRulesCorpus ./internal/policy`).

The 2026-10-20 reports additionally cover
[shared Redis time](measurements/shared-store-clock.md),
[lease ownership](measurements/lease-lifecycle.md),
[unattempted-reservation settlement](measurements/reservation-settlement.md)
and [independent evidence verification](measurements/measurement-verification.md).
Historical figures retain their original conditions and do not measure the
current checkout. `make chaos-local` validates the harness with nginx
round-robin; it does not measure health-based cloud failover.

Local measurement commands keep one run id for execution and reporting, drain
all gateway replicas before collecting their events, and attempt verification
even when k6 fails. The first failing stage determines the exit code, with k6
first. The limiter verifier requires exact agreement on admitted, rejected and
unexpected counts for its run and tenant. Chaos compares main successes
(including setup and retries), split admissions/rejections and completed
streams by region. Local comparisons are exact; cloud main successes retain
one shared tolerance of five events across regions. Platform, transport and
pre-admission readiness refusals are reported without requiring cost events.

## CI and image publication

The [CI workflow](../.github/workflows/ci.yml) runs on pull requests and
pushes to `main`. Its required `ci-gate` succeeds only when all mandatory
jobs succeed:

| Area | Checks |
|---|---|
| Go | golangci-lint, go vet, race tests, build and measurement fixtures |
| Documentation | Repository documentation guard |
| Infrastructure | Terraform formatting, validation and foundation mock tests |
| Security | govulncheck, actionlint, zizmor and PR dependency review |
| Static analysis | CodeQL for Go and Actions |
| Images | Trivy for both image targets; fixable HIGH/CRITICAL findings fail CI |

Actions are pinned to full commit SHAs; Docker build/runtime bases use
digests. Checkout credentials are not persisted. Dependabot checks Go,
Docker, Actions and Terraform dependencies weekly. See the [security policy](../SECURITY.md)
for private reporting and support boundaries.

Publication is conditional on a successful push-to-main gate and both GCP
repository variables being configured. An unset variable skips publication.
Pull requests do not publish images. The reusable publisher checks out the
verified SHA, uses job-scoped OIDC and publishes only full commit-SHA tags;
it does not deploy the runtime.

## Cloud measurement environment

Terraform creates infrastructure only when its apply targets are invoked.
These modules are temporary measurement tooling, not production deployment
templates. The steps below incur cloud resource charges.

The cloud side is declared as code in [`infra/terraform/foundation/`](../infra/terraform/foundation/)
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
the Terraform outputs. A push to `main` publishes
`pusaq` and `mockprovider` images tagged with the verified commit SHA only
after all mandatory CI checks succeed
([`release-image.yml`](../.github/workflows/release-image.yml)). CI checks
Terraform formatting and validity without credentials. Publication uses a reusable
workflow with job-scoped OIDC permissions; federation requires the numeric
repository and owner IDs, main ref, push event, and both approved workflow paths.

The publisher's registry path is configured in the workflow. Keep it aligned
with the foundation outputs when adapting the modules to another project.

The **runtime module** ([`infra/terraform/runtime/`](../infra/terraform/runtime/))
is the two-region deployment a failover measurement runs against, and it
exists only for that run: the gateway in two Cloud Run regions (one
instance each, readiness probe on `/readyz`, Direct VPC egress), one
Memorystore Redis per region, the mock provider per region on internal
ingress, and a global external Application Load Balancer over serverless
NEGs that follows Cloud Run service health — the balancer stops sending new
requests to a region whose instances fail their readiness probe. Plain HTTP
on a global address, throwaway API keys, a generated admin token; nothing
here is a standing deployment.

```sh
cp infra/terraform/runtime/terraform.tfvars.example infra/terraform/runtime/terraform.tfvars
make runtime-init && make runtime-plan && make runtime-apply
make chaos-run CHAOS_EXPERIMENT=none
make chaos-run
make runtime-destroy
```

Before applying, set `image_tag` to an actually published **40-character
lowercase commit SHA** in the runtime variables file. Mutable tags and
abbreviated SHAs are rejected. Destroy the runtime the same day as the run.

`make chaos-run` drives [`loadtest/chaos.js`](../loadtest/chaos.js) with k6 in
Docker from the machine it runs on, collects the gateways' events from Cloud
Logging ([`fetch-events.sh`](../loadtest/fetch-events.sh)), and computes the
numbers ([`verify-chaos.sh`](../loadtest/verify-chaos.sh)); `make chaos-local`
runs the same harness on the Compose profile at no cost (nginx has no health
checks there, so it checks the plumbing, not the failover).
