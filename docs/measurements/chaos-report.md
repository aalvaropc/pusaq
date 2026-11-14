# Chaos report: region, provider and store failures

Three failures injected on purpose under load, each with its hypothesis,
injection, what broke, cause, fix and verification. Every number below was
measured in the run its source report describes, on the date given; nothing
here is a guarantee for another deployment or for the current checkout.
Failure modes that were not run are listed as not measured.

| Case | Injection | Date and environment | Headline | Source |
|---|---|---|---|---|
| Region failure | Region A's readiness failed for 60 s through the admin endpoint (the instance keeps running) | 2026-10-07; Cloud Run in two regions behind a global load balancer | New requests answered by B 4.1–5.6 s after the flip; one retry recovered 41 of 45 first-attempt failures; 0 interrupted streams; split quota +5 % over one region's share | [regional-failover.md](regional-failover.md) |
| Provider failure | Primary mock answers 500 for 5 s or 15 s | 2026-09-13; Compose, two replicas, zero-latency mocks | Breaker opened 0.834–0.918 s after the failure started and closed 5.918–6.084 s after it ended; 0 client errors | [breaker-detection.md](breaker-detection.md) |
| Store outage | Redis paused (hung) or stopped (down) for 30 s | 2026-11-14; Compose, two replicas, one Redis | Each store call bounded at 250 ms; the fail-open tenant got ≈ 304–320 requests over its configured rate; 147–165 budget increments lost and logged; recovery after a stop 0.009–4.859 s per replica | [redis-outage.md](redis-outage.md) |

## Region failure

Source: [regional-failover.md](regional-failover.md), runs of 2026-10-07
(one control run and three injected runs; runs 2 and 3 are the reference,
run 1 lost 45 iterations to an undersized k6 pool).

- **Hypothesis.** Failing region A's readiness takes its only instance out
  of rotation within one 2 s probe period, and the balancer moves new
  requests to B some seconds after that. Before the platform notices, A's
  gateway refuses new requests itself with a fast, retriable 503
  `not_ready`. Streams in flight complete, because the instance keeps
  running. A tenant whose quota is split per region overruns one region's
  share by a bounded amount.
- **Injection.** At 60 s of a 300 s run, `POST /admin/readiness?state=down&for=60s`
  on region A's own URL, with 4 rps of completions (one retry on platform,
  `not_ready` or transport failures), 2 rps of streams, 8 rps for the split
  tenant and one readiness probe per second per target.
- **What broke.** New requests kept reaching A until B answered at +5.50,
  +4.11 and +5.61 s (runs 1–3). First-attempt failures: 32, 21 and 24. The platform held
  134 requests (runs 2 + 3) for 10.1–11.4 s before answering 429; with a
  10 s client timeout (run 1) the same wait surfaced as 26 transport
  failures. The longest interval between successful sends was 4.0–5.5 s.
- **Cause.** The balancer follows Cloud Run service health, which follows
  the readiness probe and then propagates; meanwhile Cloud Run holds
  requests for a region without a ready instance about 10 s before
  answering 429. Quotas are per region, so B's bucket adds its burst and
  refill to what A admitted.
- **Fix.** Readiness is an in-process flag behind a tight probe (every 2 s,
  one failure); the gateway refuses with 503 `not_ready` and `Retry-After`
  during an injected window; the client retries once; k6 sizes its virtual
  users to rate × the longest iteration.
- **Verification.** Runs 2–3: 41 of 45 first-attempt failures recovered by
  one retry, main success 99.8 %, streams complete / interrupted / refused
  599 / 0 / 1 and 601 / 0 / 0, B admitted 322 and 321 of the split tenant's
  requests during the window (bound 350), totals 1 623 and 1 627 against a
  one-region ceiling of 1 550. k6 counts, probes and cost events agreed;
  fail-open warnings 0.

## Provider failure

Source: [breaker-detection.md](breaker-detection.md), runs of 2026-09-13;
the 2026-10-20 repeat in [shared-store-clock.md](shared-store-clock.md#breaker-and-fallback)
recorded detection 0.834–0.875 s and recovery 5.917–5.960 s.

- **Hypothesis.** With window 20, threshold 0.5 and 10 minimum samples, the
  breaker opens on the 11th consecutive failure: 0.83–0.92 s at 12 requests/s
  per replica; it closes one cooldown (10 s) plus one inter-arrival after
  opening, about 5.9 s after a 5 s failure ends; the fallback carries every
  request, so no client sees a 502.
- **Injection.** `POST /admin/fail?for=5s` (and `15s`) on the primary mock at
  t = 20 s, with three tenants at 8 rps each through two replicas for 45 s.
- **What broke.** 11 requests per replica (12 with the 15 s window) paid one
  failed attempt against the primary before the breaker opened; the 15 s
  window reopened the breaker once from half-open.
- **Cause.** The breaker needs ⌊W·θ⌋ + 1 = 11 failures in its window before
  it opens; until then every request tries the primary first.
- **Fix.** A circuit breaker per upstream backend and fallback to an
  equivalent backend before the first response byte.
- **Verification.** Detection 0.834–0.918 s, open 10.084–20.166 s, recovery
  5.918–6.084 s after the failure ended, 0 502s, 1 080–1 081 client requests
  all 200 with p99 ≈ 3.5 ms across the run.

## Store outage

Source: [redis-outage.md](redis-outage.md), three runs of 2026-11-14, each
with a hung and a down arm.

- **Hypothesis.** Hung: every store call waits for its 250 ms budget (two
  calls for the fail-open tenant, four for the budgeted one, one before a
  fail-closed 503). Down: connections refused, fast failures. The fail-open
  tenant, offered twice its limit, is admitted at its offered rate; every
  spend increment in the window is lost and logged; the store is used again
  on the first call after the restore.
- **Injection.** `docker compose pause redis` or `docker compose stop redis`
  30 s into a 90 s run, restored 30 s after; a fail-open tenant at 20 rps
  against a 10/s limit, a budgeted tenant and a fail-closed tenant at 5 rps
  each, through two replicas.
- **What broke.** Hung: p50 503.5–506.4 ms (open), 1 004.6–1 006.4 ms
  (budgeted), 252.0–253.4 ms for 150–151 fail-closed refusals, for the whole
  window. Down: deadline-bound failures for the first 7.4–11.8 s, then
  failures in milliseconds. Both: 303.7–320.3 open-tenant admissions over
  r·W; 147–165 budget increments (4.998–5.610 USD of 15.334 USD) never
  counted. After a stop, one replica kept failing open for 0.733–4.859 s
  while the other recovered in 0.009–0.283 s.
- **Cause.** A paused Redis accepts connections and never answers. A
  stopped Redis drops out of Docker's DNS, whose answer for the missing name
  takes 0.1–0.2 s from the host's external resolvers; after 200 failed dials
  the Redis client returns errors without dialing and probes once per
  second, and that probe decides each replica's recovery.
- **Fix.** The store client has no internal retries and ends every
  operation at the context deadline; each store call runs under the 250 ms
  limiter budget; the tenant's policy picks fail open (with `fail_open` on
  the cost event) or fail closed (503 `store_unavailable`); a failed spend
  increment is logged with its request.
- **Verification.** Every answered request matched one cost event; every
  `fail_open` entry matched a gateway warning; no fail-closed request was
  admitted without a decision; the spend counter equalled Σcost − Σlost in
  all six arms; 0 gateway 5xx other than `store_unavailable`, 0 transport
  failures, 0 dropped iterations.

## Not measured

- **Hard regional failure.** Killing the instances or the region, and the
  fate of in-flight streams under it: not measured; the region case is a
  soft readiness failure.
- **Load balancer outlier detection** (ejecting a backend on consecutive
  5xx): off in every regional run; not measured.
- **More than one instance per region, other client locations, HTTPS** on
  the regional deployment: not measured.
- **Provider failures other than fast 500s**: timeouts, a slow but healthy
  provider, partial error rates and failures in the middle of a stream; not
  measured. The zero-latency mock is the optimistic floor for detection.
- **Store failures other than pause and stop**: a managed Redis failover, a
  partition that drops packets, a store slower than usual but within the
  budget, and a resolver that fails the missing name at once; not measured.
  Lease mode under a store outage: not measured.
- **Per-region stores during a store outage**: the local runs use one Redis
  for both replicas; not measured.
