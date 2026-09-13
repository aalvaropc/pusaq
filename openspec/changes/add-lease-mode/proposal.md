# Proposal: add-lease-mode

## Why

Strict mode pays one Redis round-trip per request, admitted or not — the measured cost of an exact limit. Lease mode is the documented alternative (docs/design.md §5.2): a replica takes a batch of K request tokens in one atomic operation and admits locally, trading a bounded, declared overrun for K× fewer round-trips. Building it behind a flag and measuring both modes with the same harness turns "when to choose which" from an opinion into two rows of numbers.

## What Changes

- `PUSAQ_RATELIMIT_MODE=strict|lease` (default `strict`), `PUSAQ_LEASE_SIZE` (K, default 10, at most the request bucket capacity), `PUSAQ_LEASE_TTL` (default 1s). The startup log names the mode.
- A third atomic Lua operation, *take*: grants between `need` and `want` whole tokens in one round-trip, never more than the bucket holds; the strict script is unchanged.
- A lease-mode limiter for the request dimension: per-tenant local tokens with expiry, a janitor that refunds expired leases to the shared bucket, refund of everything held on graceful shutdown, and local caching of a denial until the store's retry time (so an overloaded tenant costs one round-trip per retry interval, not per request). Token limits (TPM) stay strict: their costs are variable and their reconciliation cannot be batched.
- The published bound: cumulative admissions from a full bucket never exceed the bucket; within a window the admitted count may exceed strict accounting by at most `M·K` for M replicas; a replica that dies without draining loses at most K tokens per tenant — under-admission, never overrun.
- The load-test harness gains `LOADTEST_MODE` and `LOADTEST_LEASE_SIZE`; its lower bound accounts for tokens still held by replicas; `docs/measurements/limiter-exactness.md` gains the lease rows next to the strict ones.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `rate-limiting`: a new requirement for lease mode with its bound, refund, denial-caching, and configuration behavior; the exactness requirement is scoped to strict mode.

## Impact

- Modified: `internal/ratelimit` (take script, `Leased`), `internal/config`, `cmd/pusaq` (wiring, shutdown refund), `docker-compose.yml`, `loadtest/limiter.js`, `Makefile`, README, `docs/design.md` §5.2, `docs/measurements/limiter-exactness.md`.
- No new dependencies.
