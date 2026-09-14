# Measurement: request-limit exactness across replicas

**Claim under test.** With rate limiting enabled, the number of requests a
tenant gets through two gateway replicas sharing one Redis is bounded by its
bucket — capacity plus refill — no matter how hard the tenant pushes. And the
limiter's own latency toll, on requests under the limit, is small.

**Why it matters.** A local counter per replica would admit `replicas ×
limit`; the atomic Lua bucket is what makes the limit hold when the gateway
scales. This run is the evidence, produced by one command from three
independent vantage points.

## Hypothesis

Starting from a full bucket of capacity B with sustained rate r = RPM/60,
a tenant offering `RATE_MULT × r` requests per second for T seconds gets
admitted a count in `[B + r·(T − ε), B + r·(T + ε)]`, where ε = 2 s of
refill covers scheduler jitter and in-flight requests at the window edges.
Every other request is rejected with 429 `rate_limited`. A second tenant
under its own limit sees zero rejections. On requests under the limit, the
limiter adds a single Redis round-trip per request and nothing else.

## Protocol

```
make loadtest                          # defaults: RPM=600 B=100 T=120s ×10
make loadtest LOADTEST_DURATION=300s   # the 5-minute run
make loadtest-down
```

`make loadtest` starts the Compose `loadtest` profile: two rate-limited
gateway replicas (`pusaq-a`, `pusaq-b`) and two replicas with limiting
disabled (`pusaq-nolimit-a`, `pusaq-nolimit-b`) behind an nginx round-robin
balancer, a zero-latency mock upstream, and Redis. It flushes Redis (the
bound assumes a full bucket at t = 0), resets Redis command statistics, runs
`loadtest/limiter.js` with k6 in-network, then runs `loadtest/verify.sh`.

k6 scenarios (constant arrival rate):

| Scenario | Tenant | Target | Rate | Window | Checks |
|---|---|---|---|---|---|
| `exactness` | A | limited replicas | `RATE_MULT × RPM/60` rps | T | admitted within bound; only 200/429; `dropped_iterations == 0` |
| `neighbor` | B | limited replicas | 2 rps | T (same window) | zero 429 |
| `toll_limited` | C | limited replicas | 8 rps | 60 s after the overload | zero 429; p50/p95/p99 |
| `toll_unlimited` | C | unlimited replicas | 8 rps | same window as `toll_limited` | p50/p95/p99 |

Ground truth, three vantage points that must agree:

1. **k6** counts 200 vs 429 per scenario and enforces the bound as thresholds
   (the run exits non-zero if violated).
2. **Mock provider** counts requests per requested model (`GET /stats`);
   each alias routes to a distinct physical model name in this profile, so
   the per-model delta between setup and teardown is the served count per
   scenario.
3. **Gateway cost events** (`cost_event` JSON lines on each replica's stdout)
   give admitted/rejected per replica, the replica split, and the count of
   fail-open admissions (must be zero).

`verify.sh` also reads Redis `INFO commandstats` for Lua script calls, from
which it reports script calls per limited request.

Toll traffic is sent as `pusaq/auto` with `metadata.feature_tag: "toll"`
and a routing rule that sends it to `pusaq/best`, so it also exercises the
router under load (a wrong `X-Pusaq-Route-Reason` fails the run) and shares
the mock's second physical model with the neighbor tenant; only the
overload tenant's model is isolated.

Token limiting is disabled for this profile (`PUSAQ_TPM_LIMIT=0`) so the
measured toll is the request limiter's alone; with it on, every admitted
request would pay a second script call plus a reconciliation call.

## Environment

| Item | Value |
|---|---|
| Host | Linux 7.1 (Fedora), Intel Core i5-13600K (20 threads), 31 GiB; all containers on this one host |
| Docker / Compose | Docker 29.8.0, Compose v5.5.1 |
| Images | `redis:7-alpine`, `nginx:1.27-alpine`, `grafana/k6:1.4.0`, gateway and mock built from the commit under test |
| Replicas | 2 limited, 2 unlimited, one nginx worker |
| Mock latency | 0 ms, error rate 0 |
| Limiter time budget | 250 ms per store call (fail-open past it) |

## Results

Runs of 2026-09-13 at the default parameters (RPM 600 → r = 10/s, B = 100,
×10 → 100 requests/s offered) for 120 s and 300 s. Every run: k6 thresholds
passed, `verify.sh` OK.

### Exactness

| Run | Mode | RPM / B / T | Offered | Admitted | Bound | 429 | Mock served | Admitted per replica (a / b) | Neighbor 429 | Fail-open | Lua calls | Calls per limited request | Cost events / requests |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| `strict-120s` | strict | 600 / 100 / 120s | 12001 | **1299** | [1280, 1320] | 10702 | 1299 | 670 / 629 | 0 of 241 | 0 | 12723 | 1.000 | 12001 / 12001 |
| `strict-300s` | strict | 600 / 100 / 300s | 30001 | **3099** | [3080, 3120] | 26902 | 3099 | 1581 / 1518 | 0 of 601 | 0 | 31082 | 1.000 | 30001 / 30001 |
| `lease-120s` | lease (K=10) | 600 / 100 / 120s | 12001 | **1299** | [1260, 1320] | 10702 | 1299 | 663 / 636 | 0 of 240 | 0 | 4066 | 0.320 | 12001 / 12001 |
| `lease-300s` | lease (K=10) | 600 / 100 / 300s | 30000 | **3099** | [3060, 3120] | 26901 | 3099 | 1520 / 1579 | 0 of 600 | 0 | 9896 | 0.319 | 30000 / 30000 |

Reading: the bucket started full (100) and refilled at 10/s; over 120 s
that is 1 300 admissions and the gateways admitted 1 299 — through two
processes, from 12 001 offered. The mock provider counted the same 1 299;
the cost events on the two replicas add up to the same 1 299 (split
≈ 52/48 by nginx round-robin); zero fail-open. The 300 s run lands at 3 099
of a nominal 3 100. In strict mode, one Lua call per request that reached a
limited replica, admitted or rejected.

Lease mode (`PUSAQ_RATELIMIT_MODE=lease`, `PUSAQ_LEASE_SIZE=10`,
`PUSAQ_LEASE_TTL=1s`) admitted the same 1 299 and 3 099: under sustained
overload every lease is spent as soon as it is taken, so nothing sits unspent
at the end of the window and the floor of the bound (lowered by
`replicas × K = 20`) is never approached. The ceiling is unchanged — tokens
are taken before they are spent — and the runs stay under it. Lua calls
drop to 0.32 per request, 3.1× fewer than strict. The reduction under
overload is bounded by the retry interval, not by K: with r = 10/s a denied
replica is told to retry in ~100 ms, so each replica still makes ~10 store
round-trips per second while overloaded (≈ 2 400 of the 4 066 calls in the
120 s run); the remaining calls are leases for the two tenants under their
limit, one per K admissions, plus refunds.

### Limiter toll (requests under the limit, same clock window)

| Run | Mode | Limited p50 / p95 / p99 (ms) | Unlimited p50 / p95 / p99 (ms) | Delta p50 / p95 / p99 (ms) | 429 in toll arms |
|---|---|---|---|---|---|
| `strict-120s` | strict | 1.79 / 3.51 / 4.23 | 1.71 / 3.46 / 4.09 | 0.07 / 0.05 / 0.14 | 0 |
| `strict-300s` | strict | 2.21 / 3.07 / 3.79 | 1.91 / 2.63 / 3.41 | 0.31 / 0.44 / 0.38 | 0 |
| `lease-120s` | lease (K=10) | 2.06 / 2.92 / 3.28 | 1.97 / 2.69 / 3.20 | 0.09 / 0.22 / 0.09 | 0 |
| `lease-300s` | lease (K=10) | 1.95 / 2.74 / 3.05 | 1.94 / 2.46 / 2.72 | 0.01 / 0.28 / 0.33 | 0 |

Reading: the request limiter's toll on this host is a fraction of a
millisecond at every percentile — one Redis round-trip (Redis reported
~0.13–0.19 ms per Lua call) on a ~2 ms path. Run-to-run variation between
the two rows (0.1 vs 0.4 ms) is of the same order as the toll itself; the
number to quote is "under half a millisecond on a local Redis", not a
single figure. Lease mode's toll is indistinguishable from strict mode's
here: on a local Redis the round-trip it saves is already sub-millisecond,
so its benefit shows in store load (calls per request), not in client
latency. Against a networked Redis the saved round-trip is what would move
these percentiles.

## Caveats

- **Redis in Docker on one host is an optimistic floor** for the toll: a
  managed Redis instance adds network latency this run does not see
  (docs/design.md §6). The number here isolates the limiter's mechanics, not
  a cloud round-trip.
- **One host, one clock.** Both replicas read the same clock; skew between
  replicas is not exercised. The bucket stores the replica-supplied
  timestamp by design (docs/design.md §5.2).
- **Overload latency is not a latency number.** Rejected requests do no
  upstream work, so p99 during the `exactness` scenario says nothing about
  the serving path. Only the toll scenarios, under the limit, compare
  latencies — in the same clock window, so host noise is shared.
- **nginx is one hop in both toll arms**, so it cancels out of the delta.
- **The bound has tolerance.** ε = 2 s of refill on each side accounts for
  arrival-rate ramp and the last in-flight requests; it is stated rather than
  hidden, and a violation of the upper bound is a failure of the claim, not
  of the tolerance.
