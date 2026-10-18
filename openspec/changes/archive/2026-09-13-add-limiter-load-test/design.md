# Design: add-limiter-load-test

## Context

The limiter (atomic Lua bucket, fail-open, 429 contract, TPM reservation/reconciliation) exists and is exact in an in-process test. Compose runs one gateway, Redis, and two mock providers. There is no load balancer, no load driver, and no upstream-side counter. See proposal.md.

## Goals / Non-Goals

**Goals:**

- One command that starts two gateway processes behind a balancer, overloads one tenant at a known multiple of its limit, and reports admitted vs. bound from three independent vantage points (k6, mock provider, gateway cost events).
- The latency toll of the request limiter isolated from everything else.
- Numbers published with their protocol and caveats.

**Non-Goals:**

- Lease mode; measuring TPM overrun (needs the token limiter on, which would mask the RPM toll); running the load test in CI (it needs Docker Compose and minutes of wall-clock; it stays a `make` target run locally and documented).

## Decisions

1. **Explicit replicas behind nginx, not `--scale`.** nginx's static `upstream { server pusaq-a:8080; server pusaq-b:8080; }` is round-robin with nothing to configure and resolves both peers at startup; `--scale` would need DNS re-resolution (`resolver` + variable `proxy_pass`), which loses the peer list. One worker process keeps the alternation strict. A 429 is not an upstream failure for nginx (`proxy_next_upstream` excludes it by default), so the overload never marks a peer down. Caddy (random balancing by default, auto-HTTPS to disable) and Traefik (Docker provider + labels) add configuration without adding evidence.

2. **Two balancer listeners, four gateways.** `:8080` → two limited replicas; `:8081` → two replicas with `PUSAQ_REDIS_ADDR` empty (limiting disabled, same image, same everything else). The toll scenarios hit both in the same clock window, so host noise is shared — one k6 run yields the comparison instead of two runs on different minutes.

3. **Token limiting off during the run.** `PUSAQ_TPM_LIMIT=0` is a new explicit disabled state (zero was rejected before). With TPM on, every admitted request costs a second Lua call plus an `Adjust` at finalize (the mock's usage never equals the charge), which would double the measured toll and the script-call count. The startup warning keeps the state loud, mirroring how disabled auth and disabled limiting already announce themselves.

4. **Ground truth from the upstream.** The mock provider counts requests per model under atomics/mutex and serves `GET /stats`. Routes in the profile map each alias to a distinct physical model name (`PUSAQ_ROUTES`), so `by_model` separates the exactness tenant's traffic from the neighbor's and the toll tenant's without forwarding tenant identity to the upstream. k6 snapshots `/stats` in `setup()` and diffs in `teardown()`.

5. **Bound with refill and tolerance.** Real processes cannot freeze the clock, so the bound accounts for refill: with r = RPM/60 and ε = 2 s, `UPPER = B + r·(T + ε)`, `LOWER = B + r·(T − ε)`. Defaults RPM 600, B 100, T 120 s, ×10 → 12 000 requests offered, ≈ 1 300 expected admitted, bound [1 280, 1 320]. k6 thresholds on the `admitted` counter enforce both ends; `dropped_iterations == 0` proves the offered load was delivered.

6. **Verification in bash + jq, enforcement in k6.** k6 already fails the run on the bound; `verify.sh` is a short cross-check and pretty-printer: k6 summary, mock counter, per-replica admitted from `cost_event` lines, `INFO commandstats` script calls, `grep -c "failing open"` (must be 0). A Go command would add code to maintain for a 40-line report.

7. **Fresh state per run.** The target flushes Redis (bound assumes a full bucket at t = 0) and resets command statistics (script calls then cover this run only). k6 runs with the caller's uid so the summary file is not root-owned.

## Risks / Trade-offs

- **Docker Redis latency is an optimistic floor** (docs/design.md §6): the measured toll is a lower bound of what a managed instance costs. Stated in the measurements doc.
- **One host, one clock.** Both replicas share the host clock; clock skew between replicas is not exercised.
- **Overload p99 is not a latency number.** Rejected requests do no upstream work; only the toll scenarios (under the limit) compare latencies. The doc separates the two.
- **Telemetry drops under overload** would silently undercount the log-based vantage point; the profile raises `PUSAQ_TELEMETRY_BUFFER` and the verify script reports the emitter's drop counter.
