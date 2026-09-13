# Proposal: add-limiter-load-test

## Why

The rate limiter's central claim — the limit stays exact when several gateway replicas share one store — is proven today only in-process (two `Limiter` values, one in-memory Redis, a frozen clock). Nobody has pushed real gateway processes behind a load balancer at ten times their limit and counted what got through. This change builds that measurement as a single reproducible command and publishes its numbers next to the protocol that produced them.

## What Changes

- A load-test profile in Docker Compose: two gateway replicas behind an nginx round-robin balancer, a zero-latency mock upstream, and a `k6` runner — all inside the Compose network, so the default `make up` environment is untouched.
- Two additional gateway replicas with rate limiting disabled, behind a second balancer listener, so one k6 run measures the latency the limiter adds against an otherwise identical path.
- The mock provider exposes `GET /stats` (total requests, injected errors, requests per model) so the admitted count is verified from the upstream side, independently of what k6 and the gateway logs report.
- **Explicit token-limit off switch:** `PUSAQ_TPM_LIMIT=0` disables token limiting with a startup warning. Today the token limit cannot be turned off, and every admitted request pays a token charge plus a reconciliation round-trip; measuring the request limiter's own toll needs it out of the way.
- A k6 script with three concerns: exactness (one tenant at a configurable multiple of its limit for a configurable duration, admitted count checked against the bucket bound), noisy-neighbor isolation (a second tenant under its own limit must see zero rejections), and limiter toll (a third tenant under the limit against the limited and the unlimited path, p50/p95/p99 compared). Thresholds make k6 exit non-zero when the bound is violated.
- A verification script that cross-checks k6's counts, the mock's counter, the gateways' cost events, Redis script-call statistics, and the absence of fail-open events.
- `make loadtest` / `make loadtest-down`; results published in `docs/measurements/limiter-exactness.md` with hypothesis, protocol, command, environment, and caveats.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `rate-limiting`: the exactness requirement gains scenarios for process replicas under sustained overload and for co-tenant isolation, and it SHALL be verifiable by one reproducible command; the token-limit requirement gains an explicit disabled state.

## Impact

- New: `loadtest/` (nginx config, k6 script, verify script), `docs/measurements/limiter-exactness.md`.
- Modified: `internal/mockprovider` (`/stats`), `internal/config` and `cmd/pusaq` (`PUSAQ_TPM_LIMIT=0`), `docker-compose.yml` (profile `loadtest`), `Makefile`, `.gitignore`, README.
- No new Go dependencies. k6 and nginx run as pinned container images.
