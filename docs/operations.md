# Operations

Running, observing, measuring and deploying the gateway.

## Telemetry and health

Every request admitted to the pipeline emits one `cost_event` JSON line on
stdout — success, error, panic, or stream end — including requested/served
model and token usage for completions; requests refused before admission
(oversized body, malformed `run_id`, injected not-ready window) emit none. `GET /healthz` is liveness; `GET /readyz` is
readiness and turns 503 during the drain window on SIGTERM. With
`PUSAQ_ADMIN_TOKEN` set, `POST /admin/readiness?state=down&for=<duration>`
(bearer token) fails readiness on purpose for that long — `/readyz` answers
503 and new requests are refused with `not_ready` while in-flight requests
and streams finish; `state=up` ends it early. It is the failure-injection
knob chaos runs use instead of deleting a service; each action is one
`readiness_window` log record.

## Local environment

Requirements: Go 1.26+, Docker with Compose, golangci-lint.

```sh
make up      # gateway + Redis + mock provider
make run     # run the gateway on :8080
make smoke   # POST /v1/chat/completions with pusaq/fast
make test    # go test -race -cover ./...
make lint    # golangci-lint + go vet + lint-docs
make down    # stop the local environment
```

The mock provider's admin routes are unauthenticated: `POST /admin/fail?for=5s` fails every completion for 5 s, `DELETE /admin/fail` clears it, `GET /stats` counts what reached it.

## Measurements and how to run them

Every property the gateway claims is measured under load or under injected
failure, by one reproducible command, and published with its protocol in
[`docs/measurements/`](measurements/):

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
  phrasing, temporal cues) on a 176-row labeled EN/ES corpus with hard
  negatives, against floors the test suite enforces
  (`go test -v -run TestContentRulesCorpus ./internal/policy`).

## Cloud

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
the Terraform outputs. From then on every push to `main` publishes
`pusaq` and `mockprovider` images tagged with the commit SHA and `main`
([`release-image.yml`](../.github/workflows/release-image.yml)). CI checks
Terraform formatting and validity without credentials.

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
cp infra/terraform/runtime/terraform.tfvars.example infra/terraform/runtime/terraform.tfvars   # pin the image SHA
make runtime-init && make runtime-plan && make runtime-apply       # ~10 min
make chaos-run CHAOS_EXPERIMENT=none                               # control run
make chaos-run                                                     # fail region A's readiness under load
make runtime-destroy                                               # the same day
```

`make chaos-run` drives [`loadtest/chaos.js`](../loadtest/chaos.js) with k6 in
Docker from the machine it runs on, collects the gateways' events from Cloud
Logging ([`fetch-events.sh`](../loadtest/fetch-events.sh)), and computes the
numbers ([`verify-chaos.sh`](../loadtest/verify-chaos.sh)); `make chaos-local`
runs the same harness on the Compose profile at no cost (nginx has no health
checks there, so it checks the plumbing, not the failover).
