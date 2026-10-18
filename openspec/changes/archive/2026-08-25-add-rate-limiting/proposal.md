# Proposal: add-rate-limiting

## Why

Per-tenant request limits are the gateway's first governed resource and the project's centerpiece claim: a limit that stays exact when M replicas share it. The naive read-decide-decrement against shared state is a textbook race; this change implements the token bucket as a single atomic Redis Lua script — the design the design doc calls "the heart of the project" — and proves its exactness under concurrency with tests, not assertions.

## What Changes

- Distributed RPM rate limiting: one token bucket per tenant in Redis, decided by an atomic Lua script (lazy refill, bucket starts full, consume-if-enough, TTL on idle buckets, computed `retry_after`), shared by all gateway replicas.
- A rate-limit pipeline stage after Auth: denials are a new normalized `rate_limited` error (429) carrying `retry_after_ms` in the body and a `Retry-After` header.
- **Fail-open degraded mode:** if Redis is unreachable the request passes with a logged warning — a gateway that rejects everything because its semaphore died trades a cost problem for an availability outage (docs/design.md §5.2). The choice is documented and observable.
- Rate limiting is enabled by configuring `PUSAQ_REDIS_ADDR`; unset means disabled with a startup warning (the same explicit dev-mode pattern as auth). Limits: `PUSAQ_RPM_LIMIT` (sustained requests/minute) and `PUSAQ_RPM_BURST` (bucket capacity), applied per tenant; per-tenant overrides are a policy concern.
- The gateway joins Docker Compose (it finally consumes Redis), wired to Redis and the mock provider inside the network.
- First external Go dependencies, each earned by this change: `redis/go-redis` (client) and `alicebob/miniredis` (test-only in-memory Redis with Lua support, keeping CI self-contained).

## Capabilities

### New Capabilities

- `rate-limiting`: per-tenant distributed request limits — exactness across replicas, denial contract, and degraded-mode behavior.

## Impact

- New code: `internal/ratelimit` (Lua script + limiter); rate-limit stage in `internal/gateway`.
- Modified: `internal/core` (`rate_limited` code, `RetryAfter` on the normalized error and its rendering), `internal/config` (Redis address + limits), `cmd/pusaq` (wiring), `docker-compose.yml` (pusaq service), README, Makefile.
- go.mod: `github.com/redis/go-redis/v9`; `github.com/alicebob/miniredis/v2` (tests only).
