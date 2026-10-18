# Proposal: add-circuit-breaker

## Why

A provider outage today is inherited downtime: every request wastes its full timeout against a dead upstream and fails. The design doc's resilience story (docs/design.md §5.6) turns that into a log line — a circuit breaker per backend stops hammering the dead, and fallback routes traffic to an equivalent backend. This completes the resilience scope: circuit breaker and fallback against the mock provider.

## What Changes

- **Circuit breaker per upstream backend**: closed / open / half-open. Opens when the failure rate over a sliding window crosses a threshold — with a minimum sample size (2 failures out of 2 requests is not a signal); while open, requests to that backend fail fast without being attempted; after a cooldown, half-open lets a bounded number of probe requests through — success closes, failure reopens.
- **Fallback chain**: routes gain ordered fallback backends. On an upstream error or an open breaker, the completions flow tries the next backend (at most once each); the model contract is preserved (equivalent model elsewhere). Streams fall back only before the first byte — a started stream is never silently restarted.
- **Multiple upstreams from config**: `PUSAQ_UPSTREAMS="name=url,name2=url2"` (first is primary, the rest are fallbacks in order) replaces `PUSAQ_MOCK_URL`. All upstreams speak the OpenAI-compatible surface.
- Cost events record the backend that actually served (`served_provider`) and the number of attempts, so failover is observable from the event stream.
- The mock provider now echoes the requested model (realistic) and Compose runs a second mock instance as the fallback backend.

## Capabilities

### New Capabilities

- `resilience`: circuit breaking and fallback behavior of upstream calls.

### Modified Capabilities

- `cost-telemetry`: events record the serving backend and attempt count.

## Impact

- New code: `internal/breaker`.
- Modified: `internal/gateway` (fallback loop in the completions flow), `internal/core` (served provider / attempts on envelope and events), `internal/config` (`PUSAQ_UPSTREAMS`), `internal/provider` (no change to the interface), `internal/mockprovider` (echo model), `cmd/pusaq`, docker-compose (second mock), README. **BREAKING (config, pre-1.0):** `PUSAQ_MOCK_URL` is replaced by `PUSAQ_UPSTREAMS`.
- No new dependencies.
