# Measurement: regional failover under load

**Claim under test.** When one of Pusaq's regions stops being ready, the
global load balancer sends new requests to the other region; requests
caught in the switch either fail fast and are recovered by one retry or
reach the other region; requests and streams already in flight finish. Every
one of those outcomes is counted, not estimated, and quotas stay per-region
(split), never "globally exact" (constitution rule 6).

**Why it matters.** A gateway in front of every LLM call is a single point
of failure unless a region can disappear without taking traffic with it.
The number that matters is not "zero dropped requests" — the architecture
cannot promise that — but how long new traffic keeps landing on the dead
region, what clients see meanwhile, and what one retry buys back.

## Hypothesis (written before the run)

From the design (docs/design.md §5.6, §7) and the change's design notes:

- Failing region A's readiness makes Cloud Run take its instance out of
  rotation within one probe period (2 s) and the balancer's service health
  move new requests to B some seconds after that; that propagation time is not
  published, so it is the main output.
- Before Cloud Run notices, A's gateway refuses new requests itself with
  503 `not_ready` (fast, retriable); after it notices and before the
  balancer moves, requests sent to A are answered by the platform, not the
  gateway.
- Streams in flight complete: a readiness failure is a *soft* failure — the
  instance keeps running. Zero interrupted streams is the expected result
  for this failure mode and says nothing about a hard kill.
- The split tenant is limited per region; during failover it consumes B's
  bucket on top of what A admitted, a bounded overrun.

## Protocol

The results below describe the 2026-10-07 deployment. Current runtime
configuration requires a published 40-character lowercase image SHA; the
abbreviated image identifier in the historical environment table is a run
identifier, not a valid value for the current `image_tag` setting. See
[operations](../operations.md#cloud-measurement-environment) for setup.

```
make runtime-init && make runtime-apply          # infra/terraform/runtime: two regions, global LB
make chaos-run CHAOS_EXPERIMENT=none   CHAOS_QPS=4                                        # control
make chaos-run CHAOS_EXPERIMENT=region-fail CHAOS_QPS=4 CHAOS_SPLIT_QPS=8 CHAOS_TIMEOUT=30s
make runtime-destroy
```

k6 runs from one client against the balancer for 300 s. Every request carries
`metadata.run_id`; every answer carries `X-Pusaq-Region` when a gateway
produced it. Arms:

- **main** — 4 rps of non-streaming completions (tenant-main). On a
  platform answer (429/5xx without a gateway region), a `not_ready`, or a
  transport error it waits `Retry-After` (≤ 2 s, else 0.5 s) and retries
  **once**.
- **stream** — 2 rps of streamed completions lasting ~2.4 s each (the mock
  spaces chunks 200 ms apart), so dozens are in flight at the flip;
  complete / interrupted (200 then cut) / refused (non-200 before the
  first byte, retried once).
- **split** — a tenant offered more than one region's share of its quota
  (per region: 300/min, burst 50).
- **probe** — `GET /readyz` on each region's own URL and on the balancer,
  once a second: an independent timeline.
- **flip** — at 60 s, `POST /admin/readiness?state=down&for=60s` on region
  A's own URL (the token-guarded admin endpoint). A is the region the
  balancer sends this client to, measured in warm-up (≥ 90 % required).

`loadtest/verify-chaos.sh` computes everything from k6's per-request points
(send time = completion minus duration, one clock) and cross-checks against
the gateways' cost events read from Cloud Logging by run id: successes per
tenant and region, the split tenant's admissions, `stream_interrupted`
events, the `readiness_window` record, fail-open warnings (must be zero).

## Environment

| Item | Value |
|---|---|
| Regions | us-central1, us-east1; region A (balancer's choice for this client) = **us-east1** in every run |
| Gateway | Cloud Run, 1 vCPU / 512 MiB, CPU always allocated, min = max = 1 instance per region; readiness probe `GET /readyz` every 2 s, timeout 1 s, failure threshold 1; image `pusaq:1f53ef8` |
| Store | Memorystore for Redis Basic 1 GB per region (direct peering, Direct VPC egress) |
| Upstream | mock provider per region, internal ingress; latency p50 200 ms / p99 800 ms; stream chunks every 200 ms |
| Balancer | global external Application Load Balancer, serverless NEG per region, Cloud Run service health; outlier detection off; plain HTTP |
| Quotas | per region per tenant: 300 requests/min, burst 50 (half the nominal 600/min) |
| Client | k6 1.4.0 in Docker on one laptop in Lima; ~0.2 s round trip to either region |
| Date | 2026-10-07 |

## Results

Times are seconds after the flip (the instant the admin call was sent).

| | Run 1 | Run 2 | Run 3 |
|---|---|---|---|
| Client timeout | 10 s | 30 s | 30 s |
| A's `/readyz` first 503 (probe) | +0.26 | +0.26 | +0.25 |
| First refused request | +0.25 | +0.36 | +0.35 |
| **First request answered by B** | **+5.50** | **+4.11** | **+5.61** |
| Balancer probe first answered by B | +7.10 | +6.00 | +7.00 |
| **Longest interval between successful sends** | **5.50** | **4.00** | **5.50** |
| Last first-attempt failure | +14.75 | +7.36 | +9.10 |
| Main first-attempt failures (`not_ready` / platform 429 / transport) | 32 (6 / 0 / 26) | 21 (2 / 19 / 0) | 24 (1 / 23 / 0) |
| **Recovered by one retry** | 26 | **19** | **22** |
| Failed after the retry | 6 (transport) | 2 (platform 429) | 2 (platform 429) |
| Main requests offered / succeeded | 1198 / 1192 | 1201 / 1199 | 1201 / 1199 |
| Streams complete / **interrupted** / refused (after retry) | 598 / **0** / 3 | 599 / **0** / 1 | 601 / **0** / 0 |
| Stream first-attempt refusals | 14 (429 ×11, 503 ×3) | 9 (429 ×8, 503 ×1) | 13 (429 ×13) |
| Traffic back on A after the window ended | +7.5 | +8.2 | +7.5 |
| Dropped iterations (k6 could not send) | **45** | 0 | 0 |

What the refusals were, by latency:

| Answer (main and split arms) | Count, runs 2 + 3 | Sent | Latency | Produced by |
|---|---|---|---|---|
| 503 `not_ready` (with `X-Pusaq-Region`) | 11 | +0.25 to +0.75 s | 106–118 ms | A's gateway, before Cloud Run took the instance out of rotation |
| 429 without a gateway region | 134 | +0.61 to +7.6 s (one straggler at +19.3 s in run 3) | **10.1–11.4 s** | Cloud Run, after holding the request ~10 s with no ready instance in A |

Latency of successful main requests, first attempts (p50 / p95 / p99, ms):

| Run | A before the flip | B during the window | A after |
|---|---|---|---|
| Control (no flip) | 338 / 613 / 860 | — | — |
| Run 1 | 338 / 603 / 804 | 479 / 785 / 936 | 337 / 568 / 826 |
| Run 2 | 333 / 533 / 788 | 480 / 668 / 968 | 340 / 641 / 875 |
| Run 3 | 328 / 604 / 861 | 493 / 790 / 920 | 339 / 579 / 824 |

Split tenant (per-region bucket 300/min, burst 50; a single region over
300 s admits at most 50 + 5 × 300 = 1 550):

| Run | Offered | Admitted A | Admitted B | B during the window (bound 50 + 5 × 60 = 350) | Total |
|---|---|---|---|---|---|
| Control (15 rps) | 4 501 | 1 549 | 0 | — | 1 549 |
| Run 2 (8 rps) | 2 356 | 1 253 | 370 | 322 | 1 623 |
| Run 3 (8 rps) | 2 350 | 1 263 | 364 | 321 | 1 627 |

Every run: k6 counts, probe timeline and the gateways' cost events agree
(successes per tenant and region, the split tenant's admissions, one
`readiness_window` record); fail-open warnings 0; `stream_interrupted`
events 0.

## Reading

- **Three intervals, each with its own answer.** For the first 0.5–0.75 s
  (1.7 s in run 1) the balancer still routes to A and A's gateway refuses
  new requests itself (503 `not_ready`, ~110 ms, retriable). Then Cloud Run
  takes A's only instance out of rotation (its readiness probe runs every
  2 s); requests the balancer still sends to A are held by the platform for
  ~10 s and answered 429 with no gateway behind them. From +4.1 to +5.6 s
  the balancer sends new requests to B, though not all at once: stragglers
  kept reaching A until +7.6 s in run 2 and, for one request, +19.3 s in
  run 3. The balancer's own `/readyz` probe, sampled once a second, first
  sees B at +6.0 to +7.1 s.
- **One retry recovers almost everything.** 41 of 45 first-attempt
  failures in runs 2–3 succeeded on the retry; the 4 that did not were sent
  early enough that the retry also landed on A and met the platform's 429.
  Main success rate over the whole run: 99.8 %.
- **The cost of the switch is ~10 s for the unlucky requests**, not an
  error: a client caught in the platform's queue waits ~10 s for a 429. A
  client with a timeout at or below ~10 s sees a transport error instead —
  which is exactly what run 1 (10 s timeout) recorded as 26 transport
  failures.
- **No stream was interrupted.** As stated in the hypothesis, a readiness
  failure leaves the instance running: streams already flowing through A
  finish. Streams that had not started were refused (429/503) and recovered
  by one retry in 21 of 22 cases in runs 2–3. This says nothing about a
  region that dies abruptly; that failure mode was not run.
- **Traffic returns** to A about 7.5–8.2 s after its readiness recovers, and
  latency returns to the pre-flip distribution.
- **B costs ~150 ms at p50** for this client (farther region, not
  saturation).
- **Split quotas behave as designed and are the honest trade-off.** Under
  normal operation a client the balancer pins to one region gets that
  region's half of the tenant's quota (control: 1 549 of a 1 550 ceiling).
  During failover B's own bucket adds its burst plus its refill (322 and 321
  admitted during the 60 s window, under B's 350), so the tenant's total
  rises ~5 % above one region's share — a bounded overrun, not a global
  limit.

## Caveats

- **Run 1 is not the reference.** Its harness sized k6's virtual users for
  normal latency: when requests hung for the 10 s client timeout, k6 ran out
  of free VUs and did not send 45 iterations (3 main, 34 split, 8 probe)
  between +3.8 and +24 s, overlapping the very interval being measured. The harness
  now sizes VUs to rate × the longest an iteration can take; runs 2 and 3
  dropped none. Run 1's numbers are kept because they show what a 10 s
  client timeout experiences.
- **Injection is per process; one instance per region makes it regional.**
  With more instances, Cloud Run could route around the not-ready one
  inside the region.
- **One client, one city.** The balancer picks the region by proximity;
  from Lima that was always us-east1. Other clients would see the same
  mechanism with different regions.
- **The ~10 s platform hold before 429** is Cloud Run behavior observed in
  these runs, not a documented constant.
- **Outlier detection** (ejecting a backend on consecutive 5xx) was off in
  every run (`outlier_detection`, default false).
- **Plain HTTP** on a global address with throwaway keys: acceptable for
  synthetic traffic on a deployment that lived about an hour.
- **Clocks.** Every time above comes from k6's clock (send time =
  completion minus duration); server timestamps are used only to
  cross-check.
- **Cost.** The deployment existed for about an hour (two Memorystore
  instances, four always-allocated Cloud Run instances, one forwarding
  rule): under one dollar at the published hourly rates; the label-filtered
  budget reports the real figure.
