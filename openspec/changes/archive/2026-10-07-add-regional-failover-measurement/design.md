# Design: add-regional-failover-measurement

## Context

The gateway runs in one region locally and nowhere in the cloud; the foundation module publishes images and nothing else. Readiness exists (`/readyz`, a flag the drain flips) but nothing can fail it on purpose, and cost events carry no region or run id. The constitution forbids deleting a service as a chaos test and forbids promising zero dropped streams; the design commits to one Redis per region and split quotas. See proposal.md.

## Goals / Non-Goals

**Goals:**

- The numbers the design promises, each from a stated source: time until new requests land in the other region, the longest interval without a success, first-attempt failures / recovered by retry / failed after retry, streams complete / interrupted / refused, latency before / during / after per region, and the split tenant's admissions per region.
- A deployment that exists only for the run, under ~$10, and a harness that can be dry-run locally at no cost.

**Non-Goals:**

- A standing multi-region deployment, TLS and a domain on the balancer, Pub/Sub → BigQuery telemetry, hard-kill of an instance (a readiness failure is a soft failure: the instance keeps running), or a real LLM upstream.

## Decisions

1. **Failover mechanism: Cloud Run service health.** A global external Application Load Balancer with serverless NEG backends takes no classic health check; it follows Cloud Run service health, which marks a region unhealthy when fewer than 60 % of its instances pass their readiness probe, and needs at least one minimum instance per region. That is exactly "fail the readiness endpoint" (docs/design.md §7, constraint 4). Outlier detection (consecutive 5xx) stays available behind a variable, off by default, so a run attributes failover to one mechanism.

2. **Injection is per process; one instance per region makes it regional.** The not-ready window lives in the gateway process, so the runtime caps the gateway at one instance per region during a run. Otherwise Cloud Run could start a fresh, ready instance and the region would heal itself. Documented as a caveat, not hidden.

3. **A not-ready replica refuses new requests.** Cloud Run pulls an instance out of rotation only after `failure_threshold × period_seconds` of failed probes; without refusing, the pre-detection window would be served normally and the client-side gap would measure Cloud Run's probe timing, not a regional failure. So during the window `/readyz` answers 503 and new completions get 503 `not_ready` with a `Retry-After` for the remainder, before the body is read and without a cost event (as the oversized-body rejection). In-flight requests and streams are untouched. The SIGTERM drain keeps its semantics (serve during the drain delay): the balancer stops routing before the signal arrives, whereas an injected window emulates a region that is down.

4. **Region and run id in the events.** `PUSAQ_REGION` labels every event and every response (`X-Pusaq-Region`); `metadata.run_id` travels into the event as `run_id`, validated as a string of at most 64 characters and recorded before anything else can fail. The measurement reads events per region and per run from Cloud Logging with one filter — the auditable `run_id` docs/design.md §8 has promised since the start.

5. **Admin endpoint behind a token, gateway on public ingress.** The experiment injects on region A's own `run.app` URL and probes each region's `/readyz` independently of the balancer, so the gateway keeps `INGRESS_TRAFFIC_ALL`. The admin route exists only when `PUSAQ_ADMIN_TOKEN` is set (at least 16 characters), compares in constant time, caps a window at one hour, and logs each action as `readiness_window` — the same shape as the mock's `failure_window`.

6. **Mock on internal ingress, reached through the VPC.** The gateway's Direct VPC egress carries all traffic (`ALL_TRAFFIC`) with Private Google Access on the subnet, which is what an internal-ingress Cloud Run service requires from a Cloud Run caller; no NAT. The invoker binding is `allUsers` because the gateway sends no identity token; the ingress setting keeps the mock private.

7. **Region A is measured, not assumed.** The balancer routes by proximity and offers no per-region weights for serverless NEGs. `setup()` sends twenty warm requests and requires ≥ 90 % in one region, which becomes A; the verify step re-checks the pre-flip share from the run itself. `REGION_A` overrides it (the local dry run, where nginx alternates).

8. **Every number has its source.** Gap arithmetic uses k6's clock only (one client, one clock); the gateways' events cross-check successes per tenant and region, the `stream_interrupted` events per region, the split tenant's admissions, and the `readiness_window` records; per-region readiness probes give an independent timeline of when A turned 503, when it came back, and when the balancer first answered from B.

9. **Plain HTTP on the balancer.** No domain, no certificate: the traffic is synthetic, the keys are throwaway, the deployment lives for a day. Stated in the measurement doc.

## Risks / Trade-offs

- Service-health propagation time is undocumented; what Cloud Run answers for a region whose only instance is not ready (429, 503, or queueing up to the request timeout) is undocumented. Both are outputs of the experiment; k6's 10 s request timeout bounds the latter.
- `failure_threshold = 1` may be rejected by the API; the variable falls back to 2.
- A single client in one city may split between two US regions; the warm-up check aborts before injecting rather than measure noise.
- Expected: zero interrupted streams under a readiness failure, because the instance keeps running. That is the finding for a soft failure and is published as such; a hard kill is out of scope.
