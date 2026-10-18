# Measurement: circuit-breaker detection and recovery

**Claim under test.** When the primary upstream dies, each gateway replica's
breaker opens within a bounded, predictable number of failed attempts,
traffic keeps flowing through the fallback without a single client error,
and the breaker closes one cooldown plus one probe after the upstream is
healthy again.

**Why it matters.** Without a breaker, a dead provider costs every request
its full timeout before fallback (docs/design.md §5.6). The breaker turns
that into a fast local decision — but only if it opens quickly and closes
promptly. These are the two numbers, measured rather than asserted.

## Hypothesis

With window W = 20 (filled by the warm-up), threshold θ = 0.5, minimum
samples 10, cooldown 10 s, and 1 probe, the breaker opens on the
⌊W·θ⌋ + 1 = 11th consecutive failure. At r = 12 requests/s per replica
(three tenants at 8 rps, round-robin over two replicas), that is 11/12 s
plus at most one inter-arrival gap:

- **detection** (failure start → open) ∈ [0.83, 0.92] s;
- **open duration** = cooldown + one inter-arrival ≈ 10.0–10.1 s;
- **recovery** (failure end → closed) = open duration − failure window
  ≈ 5.9 s for a 5 s window, with **1 probe**;
- a 15 s window makes the first probe (at ≈ 10.9 s) fail: one
  half-open → open, a second cooldown, **2 probes**, closing ≈ 20.9 s
  after opening;
- **zero 502s**: the fallback carries every request the primary drops;
- **11 requests per replica pay an extra attempt** (the failures that
  tripped the breaker); 12 with the 15 s window (one failed probe).

## Protocol

```
make breakertest                        # 5 s failure window
make breakertest BREAKER_FAIL_FOR=15s   # a probe fails once
make loadtest-down
```

`make breakertest` starts the Compose `loadtest` profile (two rate-limited
replicas behind nginx, `mock-lt` as primary, `mock-lt-backup` as fallback,
both at zero latency), flushes Redis, and runs `loadtest/breaker.js` with
k6: three tenants at 8 rps each against the limited replicas for 45 s —
under their 10 rps sustained limit, so the limiter never interferes. At
t = 20 s (after the warm-up filled every breaker's window) one k6 iteration
calls `POST /admin/fail?for=<window>` on the primary mock, which then fails
every completion with 500 until the window ends and logs the window's start
and end instants. `loadtest/verify-breaker.sh` then reads:

1. the mock's `failure_window` record (`from`, `until`);
2. each replica's `breaker_state` records (`closed → open`, `open →
   half_open`, `half_open → closed` or `half_open → open`), timestamped by
   the gateway;
3. each replica's cost events: requests with `attempts == 2` (paid the
   primary before the breaker opened), `served_provider == "backup"`, and
   `error_code == "upstream_error"` (must be zero).

Detection = t(open) − t(from); recovery = t(closed) − t(until). All
containers share the host clock. k6 fails the run on any 502, any other
unexpected status, or dropped iterations; the verify script fails on a
detection above 1.5 s, a breaker that never closes, or upstream errors
reaching clients.

## Environment

| Item | Value |
|---|---|
| Host | Linux 7.1 (Fedora), Intel Core i5-13600K (20 threads), 31 GiB; all containers on this one host |
| Docker / Compose | Docker 29.8.0, Compose v5.5.1 |
| Images | `redis:7-alpine`, `nginx:1.27-alpine`, `grafana/k6:1.4.0`, gateway and mocks built from the commit under test |
| Breaker | window 20, min samples 10, threshold 0.5, cooldown 10 s, probes 1 (defaults) |
| Traffic | 3 tenants × 8 rps = 24 rps, 12 rps per replica; `pusaq/fast`; 45 s |
| Mocks | primary and fallback at 0 ms latency, error rate 0 |

## Results

Runs of 2026-09-13. Every run: k6 thresholds passed, `verify-breaker.sh` OK.

| Run | Window | Replica | Transitions | Detection | Open for | Recovery | Probes | Reopened | Extra attempts | Served by backup | 502 to clients |
|---|---|---|---|---|---|---|---|---|---|---|---|
| `breaker-5s` | 5 s | a | closed→open→half_open→closed | **0.876 s** | 10.084 s | **5.960 s** | 1 | 0 | 11 | 131 | 0 |
| `breaker-5s` | 5 s | b | closed→open→half_open→closed | **0.834 s** | 10.084 s | **5.918 s** | 1 | 0 | 11 | 131 | 0 |
| `breaker-15s` | 15 s | a | closed→open→half_open→open→half_open→closed | **0.876 s** | 20.083 s | **5.959 s** | 2 | 1 | 12 | 251 | 0 |
| `breaker-15s` | 15 s | b | closed→open→half_open→open→half_open→closed | **0.918 s** | 20.166 s | **6.084 s** | 2 | 1 | 12 | 252 | 0 |

Client side, both runs: 1080–1081 requests, all 200, none dropped; p50 /
p95 / p99 ≈ 2.1 / 3.1 / 3.5 ms across the whole run, failure window
included — the outage is invisible to clients except as 11 requests per
replica that paid one extra (failed) attempt against the primary.

Reading: every number lands where the formula put it. Detection is the
11th failure at 12 rps (0.83–0.92 s); the breaker stays open for exactly one
cooldown plus one inter-arrival (10.08 s); recovery is that minus the 5 s
window (≈ 5.9 s). With the longer window the first probe meets a still-dead
primary, reopens the breaker for a second cooldown, and the second probe
closes it — 12 extra attempts, 2 probes, recovery again ≈ 6 s after the
window ends. The fallback absorbed 131 and 251 requests per replica
respectively (the 11–12 fallbacks plus everything routed around the open
breaker).

## Caveats

- **Zero-latency mock is the optimistic floor.** Against a real provider
  each of the 11 failing attempts costs a timeout or an error round-trip;
  detection is then dominated by the upstream timeout, not by the breaker's
  arithmetic. The breaker's contribution — 11 failures at the offered rate —
  is what this run isolates.
- **Lazy half-open.** The breaker notices the cooldown has elapsed on the
  next call; under this steady traffic that is at most one inter-arrival
  (≈ 83 ms). With no traffic it stays open until the next request arrives:
  there are no active health probes.
- **One host, one clock.** Detection and recovery are differences between
  timestamps written by different containers on the same host; clock skew
  between machines is not exercised.
- **Recovery depends on the window length only through probe failures.**
  Recovery after the window ends is bounded by one cooldown plus one
  inter-arrival regardless of how long the outage lasted; a probe that
  lands inside the window adds one full cooldown.
