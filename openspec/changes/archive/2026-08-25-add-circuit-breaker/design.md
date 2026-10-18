# Design: add-circuit-breaker

## Context

Upstream failures today map to `upstream_error` and fail the request; a dead provider costs every request its timeout. The provider seam (`Provider` interface), per-attempt outcome knowledge, and cost events all exist. docs/design.md §5.6 fixes the breaker semantics (three states, minimum sample size, bounded probes). See proposal.md.

## Goals / Non-Goals

**Goals:**

- The breaker state machine exactly as specified, deterministic under test (injected clock), safe under concurrency.
- Fallback in the completions flow with the model contract preserved and failover observable in events.

**Non-Goals:**

- Hedged requests; retry budgets beyond one attempt per backend; active health probes (passive signal only); per-tenant fallback policy; mid-stream resumption.

## Decisions

1. **`internal/breaker` is pure state, no I/O.** `Allow() bool` before a call; `Record(success bool)` after. Ring buffer of the last N outcomes; open when `failures/count > threshold && count ≥ minSamples`; cooldown via injected clock; half-open grants at most K concurrent probes (counted, not timed); one probe success closes, one probe failure reopens. A mutex guards transitions — breaker decisions are cheap and rare relative to LLM latency. Defaults as package constants (window 20, min samples 10, threshold 0.5, cooldown 10s, probes 3); a configuration surface is a policy concern.

2. **Fallback is a loop in the completions flow, not a stage.** The chain is per-route configuration; each attempt: skip if `!breaker.Allow()`, call, `Record` the outcome, serve on success, continue on `upstream_error`. Non-upstream errors (validation, internal) stop immediately — they are not the backend's fault. TPM is charged once before the loop (same model contract across the chain), so constitution rule 4 ordering is untouched.

3. **Streams: fallback pre-first-byte only.** `StreamComplete` errors before returning a stream → next backend. Once a stream object exists, the existing stream accounting owns failures; restarting a stream midway would silently replay content to the client.

4. **`PUSAQ_UPSTREAMS="name=url,..."` replaces `PUSAQ_MOCK_URL`.** First entry is the primary for all aliases, the rest are fallbacks in order, every one an OpenAI-compatible HTTP upstream. Default remains a single local mock. Breaking pre-1.0 config change, done cleanly instead of carrying both.

5. **Observability: `served_provider` and `attempts` on the envelope and event.** `ServedModel` already exists; the provider name is the failover signal. The mock now echoes the requested model — realistic, and it keeps `X-Pusaq-Model` stable across failover while `served_provider` tells the truth about who answered.

## Risks / Trade-offs

- **Passive-only detection**: a backend with no traffic never recovers state visibility until probed by real requests in half-open. Accepted; active probes are out of scope for this change.
- **Per-process breakers**: replicas learn about a dead backend independently (no shared breaker state). Accepted: there is no shared breaker state.
- **Chain-wide TPM charge on total failure** stands (usage unknown). Consistent with the unknown-usage rule; bounded by bucket TTL.
