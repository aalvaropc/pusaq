# Design: add-rate-limiting

## Context

Tenants exist (auth slice); Redis has been in Compose since the foundation, unused until now. The founding document (docs/design.md §5.1–5.2) fixes the algorithm (token bucket) and the mechanism (atomic Lua, lazy refill); this slice implements the strict mode. Lease mode and TPM are separate slices. See proposal.md.

## Goals / Non-Goals

**Goals:**

- The Lua token bucket exactly as the founding doc specifies, with its exactness proven by a deterministic concurrency test.
- The stage in its permanent pipeline position (after Auth — RPM before any expensive work, constitution rule 4).

**Non-Goals:**

- TPM (reservation/reconciliation — next slice), lease/batching mode, per-tenant limit overrides (policy slice), the local conservative limit refinement of fail-open, breaker in front of Redis.

## Decisions

1. **One Lua script is the whole decision.** `HMGET tokens,ts` → lazy refill `min(B, tokens + elapsed×r)` (no background refiller; Redis is single-threaded so the script is atomic) → consume if enough → `HMSET` + `EXPIRE` → return `{allowed, remaining, retry_after_ms}`. The bucket starts **full** (a new tenant can use its burst immediately); `retry_after` is computed, not guessed. `now` is passed by the replica: `redis.call('TIME')` would remove replica-clock dependence but makes the script non-deterministic for replication — the founding doc's documented trade-off, kept.

2. **Clock is injected** (`func() time.Time`) — the exactness test freezes it, making "no refill accrues" exact rather than probabilistic, and refill tests advance it deterministically.

3. **Dependencies earned by this slice** (constitution: "no dependency without a slice that needs it"): `redis/go-redis/v9` as the client; `alicebob/miniredis/v2` (test-only) runs the real Lua path in-process so unit tests and CI need no Redis service. Local E2E uses the real Redis in Compose.

4. **Enablement mirrors auth's dev-mode pattern:** `PUSAQ_REDIS_ADDR` set → limiting on; unset → off with a startup warning. An implicit default address would produce permanent fail-open log spam on machines without Redis — worse than an explicit mode.

5. **Fail-open, logged.** On Redis error the stage admits and logs a warning. The founding doc's refinement (local `limit/M` fallback) needs replica-count awareness that does not exist yet; plain fail-open is the honest first cut and the spec only promises that much. Per-tenant `fail_closed` arrives with policies.

6. **`RetryAfter` on the normalized error.** `core.Error` gains an optional duration; the renderer emits `retry_after_ms` in the body and a ceiled `Retry-After` header. This extends the error contract once, for every future limiter (TPM, budgets).

7. **The gateway joins Compose** (finally a Redis consumer): `pusaq` service wired to `redis:6379` and the mock by service name, dev-mode auth for frictionless smoke. This also sidesteps the host→container firewall issue on the dev machine — E2E runs inside the network.

## Risks / Trade-offs

- **Fail-open means a Redis outage suspends limits entirely** until it recovers. Accepted and spec'd; the availability trade is deliberate (founding doc §11) and the refinement path is documented.
- **Per-request Redis round-trip (~0.5–1 ms same-zone)** on the hot path. Accepted: measuring strict-mode cost vs lease mode *is* the project's experiment; the lease slice adds the alternative.
- **miniredis may diverge from real Redis semantics.** Mitigated: the E2E runs the same script against real Redis in Compose.
