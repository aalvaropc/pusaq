# Measurement: Redis outage under load

**Claim under test.** When the limit store stops answering, every store
call on the request path ends within the 250 ms limiter budget. A tenant
without a policy is admitted without the store's decision (fails open) and
its cost event names each skipped decision in `fail_open`; a tenant with
`fail_closed=true` is refused with 503 `store_unavailable`; a budget
increment that cannot be written is logged as under-counted; and the gateway
uses the store again once it is back. The run counts what that costs —
admissions above the configured rate, latency, lost spend and recovery
time — instead of asserting it. With the store gone, a fail-open tenant has
no limit at all: limits are per store, never globally exact (constitution
rule 6).

**Why it matters.** Failing open is a deliberate trade: a limiter that
takes the gateway down with it causes the outage it exists to prevent. The
trade is only honest with its price measured: how much traffic gets through
unlimited, how slow requests become, which spend is never counted and how
long the effect outlasts the outage.

## Hypothesis (written before the run)

- **Hung** (Redis process frozen): every store call waits for its 250 ms
  budget. The fail-open tenant pays two calls (request limit, token charge,
  ≈ +500 ms), the budgeted tenant four (request limit, budget read, token
  charge, spend increment, ≈ +1 s), the fail-closed tenant one before its
  503 (≈ 250 ms).
- **Down** (Redis container stopped): connections are refused, store calls
  fail at once and latency stays near normal.
- The fail-open tenant, offered twice its request limit, is admitted at its
  offered rate during the window: about r·W requests above the configured
  rate (≈ 300 for 30 s at 10/s).
- Every spend increment attempted during the window is lost and logged. In
  the hung arm, increments written to the frozen socket may be applied once
  Redis resumes, leaving the counter above Σcost − Σlost.
- The gateway uses the store again on the first call after the restore.

## Protocol

```
make chaos-redis RUN_ID=redis-20261114-r1
make chaos-redis RUN_ID=redis-20261114-r2
make chaos-redis RUN_ID=redis-20261114-r3
make loadtest-down
```

`make chaos-redis` runs two arms, each its own run id (`<RUN_ID>-hung`,
`<RUN_ID>-down`), with `loadtest/redis-outage.sh`:

1. recreate Redis, both rate-limited gateway replicas, nginx and the mocks
   of the Compose `loadtest` profile, and flush Redis;
2. start `loadtest/redis-outage.js` with k6 in the Compose network for 90 s;
3. after 30 s, inject: `docker compose pause redis` (hung) or
   `docker compose stop redis` (down); after another 30 s, restore with
   `docker compose unpause redis` or `docker compose start redis`. The host
   clock is read before and after each command; a shell trap restores the
   store whatever stage fails;
4. after k6 ends, read the budget tenant's spend counter from Redis, drain
   the gateways, collect their cost events and store warnings, and run
   `loadtest/verify-redis.sh`.

Three tenants send `pusaq/fast` completions (`max_tokens: 8`) with
`metadata.run_id`:

| Tenant | Policy | Offered | Purpose |
|---|---|---|---|
| `tenant-open` | none (fails open) | 20 rps, twice its request limit | overrun over the configured rate |
| `tenant-budget` | `budget_usd=1000` (fails open) | 5 rps | budget read and spend increments |
| `tenant-closed` | `fail_closed=true` | 5 rps | `store_unavailable` refusals |

Each request is assigned to a period by the instant k6 sent it: **before**
the injection command started, **during** until the restore command
returned, **after** from then on. The verifier joins every answered request
to its cost event through the `X-Pusaq-Request-Id` response header and fails
the run when a request has no event, two events, or an event with another
tenant or outcome; when an event has no request; when k6's per-tenant
counters disagree with its points; when an event's `fail_open` entries
differ from the gateway's `failing open` warnings for that request, or
fail-closed refusals from the `failing closed` warnings; when a fail-closed
tenant's event carries `fail_open` or a fail-open tenant is refused with
`store_unavailable`; when a store failure precedes the injection or none
occurs during the window; when the spend counter is below Σcost − Σlost or
above Σcost; or when k6 drops iterations. Latency percentiles are over
answered requests and interpolate linearly between the closest ranks.

Definitions used below: **overrun** = the open tenant's admissions sent
during the window minus r·W (r = 10/s, W = window length; its bucket is
empty when the window opens because it is offered twice the rate);
**deadline-bound** failures are requests with a store failure answered in
250 ms or more; **recovery** = send time, after the restore command
returned, of the last request that met a store failure.

## Environment

| Item | Value |
|---|---|
| Host | Fedora 44, Linux 7.3.3, Intel Core i5-13600K (20 threads), 31 GiB; every container on this host, which other workloads shared during the runs |
| Docker / Compose | Docker 29.9.3, Compose 5.7.0 |
| Images | `redis:7-alpine`, `nginx:1.27-alpine`, `grafana/k6:1.4.0`; gateway and mocks built from this branch |
| Commit | gateway built from `a66bd72`, with the store-outage harness of this report; for runs 1–2 the verifier lacked one report line, and all six arms were re-verified with the published verifier. Gateway changes after `a66bd72` (bucket TTLs that follow refill, same-backend retries that are off by default) leave the store-outage path unchanged and were not measured here |
| Gateway | two replicas behind nginx round-robin; strict request limit 600/min (r = 10/s), burst 20; token limit 600 000/min, burst 100 000 (on, never binding); store client without retries, 250 ms budget per call |
| Prices | synthetic `mock-exactness` 1 000 / 2 000 USD per 1M input / output tokens: 0.034 USD per completion (12 + 11 tokens) |
| Upstream | zero-latency mock |
| Name resolution | Docker's embedded DNS; the host's resolver configuration adds a search domain and external servers 8.8.8.8 and 8.8.4.4 |
| Date | 2026-11-14 |

## Results

Every arm of every run: verification OK (all answered requests matched one
cost event, fail-open entries matched warnings, fail-closed refusals matched
warnings, counter within bounds); 0 transport failures, 0 gateway 5xx other
than `store_unavailable`, 0 dropped iterations, 0 token reconciliation
failures.

### Hung arm, run 3 (`redis-20261114-r3-hung`)

Window 30.63 s; the pause command took 0.37 s and the unpause 0.25 s.

| Period | Tenant | Sent | 200 | 429 | 503 `store_unavailable` | p50 / p95 / p99 ms | Fail-open rpm / tpm / budget |
|---|---|---|---|---|---|---|---|
| before | open | 508 | 273 | 235 | 0 | 4.82 / 14.65 / 22.60 | 0 / 0 / 0 |
| before | budget | 127 | 127 | 0 | 0 | 5.80 / 24.39 / 27.69 | 0 / 0 / 0 |
| before | closed | 127 | 127 | 0 | 0 | 5.27 / 19.70 / 30.45 | 0 / 0 / 0 |
| during | open | 613 | 610 | 3 | 0 | **506.44** / 514.46 / 522.42 | 602 / 598 / 0 |
| during | budget | 154 | 154 | 0 | 0 | **1006.41** / 1017.47 / 1025.10 | 150 / 148 / 149 |
| during | closed | 154 | 4 | 0 | **150** | **253.06** / 258.71 / 263.69 | 0 / 0 / 0 |
| after | open | 680 | 353 | 327 | 0 | 4.19 / 7.62 / 10.80 | 0 / 0 / 0 |
| after | budget | 170 | 170 | 0 | 0 | 4.51 / 8.63 / 11.99 | 0 / 0 / 0 |
| after | closed | 169 | 169 | 0 | 0 | 4.25 / 8.21 / 12.79 | 0 / 0 / 0 |

### Down arm, run 3 (`redis-20261114-r3-down`)

Window 32.57 s; the stop command took 1.59 s and the start 0.97 s.

| Period | Tenant | Sent | 200 | 429 | 503 `store_unavailable` | p50 / p95 / p99 ms | Fail-open rpm / tpm / budget |
|---|---|---|---|---|---|---|---|
| before | open | 552 | 295 | 257 | 0 | 4.31 / 8.50 / 17.75 | 0 / 0 / 0 |
| before | budget | 138 | 138 | 0 | 0 | 5.02 / 10.06 / 15.92 | 0 / 0 / 0 |
| before | closed | 138 | 138 | 0 | 0 | 4.95 / 9.83 / 17.09 | 0 / 0 / 0 |
| during | open | 651 | 646 | 5 | 0 | 5.53 / **508.95** / 512.44 | 642 / 642 / 0 |
| during | budget | 163 | 163 | 0 | 0 | 4.92 / **1010.63** / 1012.61 | 160 / 160 / 160 |
| during | closed | 163 | 3 | 0 | **160** | 3.55 / **255.66** / 257.89 | 0 / 0 / 0 |
| after | open | 598 | 325 | 273 | 0 | 4.70 / 9.88 / 17.26 | 12 / 12 / 0 |
| after | budget | 150 | 150 | 0 | 0 | 5.63 / 14.32 / 29.65 | 3 / 3 / 3 |
| after | closed | 150 | 148 | 0 | 2 | 5.54 / 14.27 / 27.53 | 0 / 0 / 0 |

### Both arms, three runs

| | Hung r1 | Hung r2 | Hung r3 | Down r1 | Down r2 | Down r3 |
|---|---|---|---|---|---|---|
| Window W (s) | 30.62 | 31.98 | 30.63 | 31.91 | 31.26 | 32.57 |
| Injection command (s) | 0.29 | 1.74 | 0.37 | 0.96 | 0.60 | 1.59 |
| First store failure after the injection started (s) | +0.27 | +1.66 | +0.27 | +0.37 | +0.15 | +0.50 |
| Open tenant: admitted during the window / r·W | 610 / 306.2 | 624 / 319.8 | 610 / 306.3 | 634 / 319.1 | 624 / 312.6 | 646 / 325.7 |
| **Overrun over the configured rate** | **+303.8** | **+304.2** | **+303.7** | **+314.9** | **+311.4** | **+320.3** |
| Open tenant: admitted over the run vs B + r·T = 920 | 1236 | 1237 | 1236 | 1302 | 1259 | 1266 |
| Fail-closed refusals during / after the window | 150 / 0 | 151 / 0 | 150 / 0 | 158 / 13 | 155 / 1 | 160 / 2 |
| Store failures in the window: deadline-bound / faster | 902 / 0 | 904 / 0 | 903 / 0 | 340 / 607 | 219 / 714 | 226 / 736 |
| Last deadline-bound failure (s after the injection) | +30.32 | +31.71 | +30.37 | +11.82 | +7.45 | +8.15 |
| Open tenant p50 during the window (ms) | 505.69 | 503.50 | 506.44 | 5.20 | 2.59 | 5.53 |
| **Budget increments lost** (warnings), USD | **147**, 4.998 | **147**, 4.998 | **147**, 4.998 | **165**, 5.610 | **158**, 5.372 | **163**, 5.542 |
| Spend counter / Σcost (USD) | 10.336 / 15.334 | 10.336 / 15.334 | 10.336 / 15.334 | 9.724 / 15.334 | 9.962 / 15.334 | 9.792 / 15.334 |
| Increments applied after being reported lost | 0 | 0 | 0 | 0 | 0 | 0 |
| Store failures sent after the restore | 0 | 0 | 0 | 74 | 16 | 17 |
| **Recovery**: last such failure, replica a / b (s) | — | — | — | **4.859** / 0.009 | **0.893** / 0.042 | **0.733** / 0.283 |
| First store decision after the restore (s) | +0.006 | +0.034 | +0.042 | +0.109 | +0.143 | +0.383 |

In the down arm each replica switched from deadline-bound to fast failures
on its own: at +11.4 / +11.8 s in run 1, +7.5 / +7.4 s in run 2 and
+7.8 / +8.2 s in run 3 (replica a / b), after 109–173 deadline-bound
requests per replica.

## Reading

### Hung: Redis paused

- **Hypothesis.** Each store call waits for its 250 ms budget; the
  fail-open tenant admits at its offered rate; frozen writes may land once
  Redis resumes; recovery on the next call.
- **Injection.** `docker compose pause redis` for 30 s under steady load.
- **What broke.** Every request of every tenant became slower by its
  store calls: p50 503.5–506.4 ms for the open tenant (two calls),
  1004.6–1006.4 ms for the budgeted one (four), 252.0–253.4 ms for the
  fail-closed tenant's 503s (one). The open tenant was admitted at its
  offered rate: 303.7–304.2 requests above r·W, 1 236–1 237 over the run
  against a limiter bound of 920. 147 budget increments (4.998 USD, a third
  of the run's 15.334 USD at the synthetic prices) were never counted, and
  150–151 fail-closed requests were refused.
- **Cause.** A frozen Redis keeps its listening socket: the kernel accepts
  connections and buffers writes, nothing answers, so each call ends only at
  its context deadline.
- **Fix.** The store client has no internal retries and ends reads, writes
  and dials at the context deadline; every store call on the request path
  runs under the 250 ms limiter budget; the tenant's policy picks fail open
  or fail closed.
- **Verification.** Latency during the window is the number of store calls
  times 250 ms plus a few milliseconds, in all three runs. No increment the
  gateway reported lost was applied after the unpause (counter =
  Σcost − Σlost exactly), contrary to the hypothesis. No request sent after
  the unpause met a store failure; the first store decision came 6–42 ms
  after the unpause command returned.

### Down: Redis stopped

- **Hypothesis.** Connection refused: fast failures, near-normal latency,
  recovery on the next call.
- **Injection.** `docker compose stop redis` for 30 s, then
  `docker compose start redis`.
- **What broke.** For the first 7.4–11.8 s the failures cost the full
  budget, as in the hung arm; then they took milliseconds (open tenant p50
  2.6–5.5 ms, p95 503–509 ms over the window). The overrun was
  311.4–320.3 requests over r·W, and 158–165 budget increments
  (5.372–5.610 USD) were lost. After the restore, one replica used Redis
  again within 9–283 ms while the other kept failing open for 0.73–4.86 s:
  16–74 more requests met a store failure, among them 1–13 fail-closed
  refusals.
- **Cause.** The hypothesis was wrong about the failure shape. Stopping the
  container removes its name from Docker's DNS: a container on the same
  network resolves `redis` with "Name does not resolve", which Docker's
  embedded DNS answers only after forwarding the name to the host's external
  servers — 0.1–0.2 s per name, measured with `getent hosts` and `nslookup`
  from a helper container while Redis was stopped. With the host's search
  domain added, the gateway's lookup outlasts the 250 ms budget, so the
  first failures are deadline-bound. The fast failures come from the Redis
  client: go-redis v9.23 counts failed dials and, after `PoolSize` of them
  (10 × GOMAXPROCS, 200 on this host), returns the last dial error without
  dialing, while one background probe per client redials once per second,
  each probe bounded by the 5 s dial timeout. That probe also decides
  recovery, separately for each replica: a replica uses Redis again only
  when its probe succeeds.
- **Fix.** The same budget bounds the slow lookups and the same policy
  applies; the gateway has no control over the client's probe schedule, and
  recovery after a stopped store depends on it.
- **Verification.** Budget-bound latency only before the client's dial
  circuit opened, fast failures after it; every lost increment logged and
  the counter equal to Σcost − Σlost in all three runs; recovery measured
  per replica rather than assumed.

### Across arms

- **The overrun is the offered excess.** With the store unreachable, the
  fail-open tenant's limit is gone: in a 30 s window it received about 300
  requests more than its 10/s rate allows, the whole of its offered excess.
  A fail-open tenant offered more would overrun by more: only its offered
  rate and the outage's length bound the overrun.
- **Fail closed holds.** No fail-closed request was admitted without a
  decision, and no fail-open request was refused, in any run.
- **Lost spend is bounded by the requests in the window**, all of them
  logged one by one: the monthly counter under-counts by exactly the logged
  increments. A budget could be exceeded by that much.

## Caveats

- **Docker's pause and stop are proxies.** A managed Redis failing over, a
  partition that drops packets (connects that time out), a store that
  answers slowly but within 250 ms, or a host whose resolver answers at once
  would each look different. On a host where the stopped name fails
  immediately, the down arm would show fast failures from the start.
- **Recovery after a stop is the client's**, not the gateway's: 0.009 s and
  4.859 s for two replicas in the same run. The 1 s probe interval and 5 s
  dial timeout are go-redis defaults this gateway does not override.
- **One Redis for both replicas.** The outage removes the limit for every
  replica at once; per-region stores are not exercised.
- **Strict mode only.** Lease mode, which holds local tokens, was not run.
- **A 10 s trial run** made while building the harness (`dbg1`, not in the
  tables) recorded, in its down arm, one increment the gateway reported lost
  that the store applied anyway (0.034 USD); the verifier accepts a counter
  between Σcost − Σlost and Σcost for that reason.
- **Synthetic prices and a zero-latency mock**: dollars and latencies
  describe this stack. With real provider latency, more requests are in
  flight when the store fails.
