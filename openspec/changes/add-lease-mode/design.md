# Design: add-lease-mode

## Context

Strict mode exists and is measured: one Lua round-trip per request, exact across replicas. The load-test harness runs two replicas behind nginx with three vantage points. The request-limit stage consumes only `Allow`; the token limiter consumes `Allow` and `Adjust`. See proposal.md.

## Goals / Non-Goals

**Goals:**

- Lease mode as a drop-in `Allow` implementation with a stated bound and observable counters.
- The strict script and the strict exactness test untouched.
- The same harness produces the comparison: admitted vs bound, script calls per request, toll p50/p95/p99, for both modes.

**Non-Goals:**

- Leasing token limits (variable costs, reconciliation on every request — leasing would keep the round-trip and add double accounting); per-tenant mode selection (a policy concern); adaptive lease sizes.

## Decisions

1. **A third script, not a parameter on the strict one.** `take` grants `max(need, min(want, ⌊tokens⌋))` when the bucket covers `need`, else denies with the usual retry. Whole tokens only: a lease is a count of admissions, and fractions stay in the bucket where refill accrues. Keeping `script` byte-identical keeps the strict measurement and its test unaffected.

2. **Per-tenant lock held across the take.** One replica makes one round-trip per K requests for a tenant, instead of N concurrent takes racing when the local lease empties. The round-trip is bounded by the stage's time budget, so the lock is held for at most that long.

3. **Denial caching is what makes overload cheap.** Without it, every rejected request would still cost a round-trip and lease mode would save nothing under overload. The cache lasts `min(retry, ttl)`: the store said no tokens accrue before `retry`, and the TTL caps how long this replica stays blind to refunds from other replicas — the under-admission this can cause is bounded by one retry interval per replica and stated in the spec.

4. **Refunds off the hot path.** A janitor ticks at `ttl/2`, moves expired tokens to a pending count under the per-tenant lock, and returns them with `Adjust` outside it. `Close` does a final sweep of everything held. A refund the store rejects is counted (`refund_failures`, `tokens_lost`): bounded under-admission, visible at shutdown, never overrun.

5. **Leftovers smaller than the cost go back, not topped up.** When a lease has fewer tokens than one request costs, the replica returns them and takes a fresh lease. One take, one whole lease; no partial arithmetic across leases.

6. **Randomized bound test with the standard library.** `math/rand/v2` with a logged seed (`PUSAQ_TEST_SEED` reproduces) drives random replica counts, lease sizes, capacities, and demand; it asserts `admitted ≤ min(demand, B)`, `admitted ≥ min(demand, B) − M·K`, and conservation after `Close` (`admitted + remaining == B`). A property-testing dependency would add nothing the invariant needs.

7. **Harness plumbing only.** `LOADTEST_MODE` and `LOADTEST_LEASE_SIZE` flow into the gateway env and into k6, which lowers the bound's floor by `REPLICAS × LEASE_SIZE`. The ceiling is unchanged: lease mode can never admit more than strict mode would.

## Risks / Trade-offs

- **Under-admission while denials are cached.** Another replica's refund can go unnoticed for up to one retry interval; accepted and stated.
- **Held tokens are invisible to the store.** Between takes, the bucket under-reports what is actually admissible by up to M·K; consumers of `Remaining` see the store's view. Stated in the spec as the per-window bound.
- **A crashed replica strands up to K tokens per tenant** until refill covers them — no worse than K requests of under-admission, never an overrun.
