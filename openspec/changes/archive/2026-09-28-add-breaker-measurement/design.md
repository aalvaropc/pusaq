# Design: add-breaker-measurement

## Context

The breaker is pure state with no I/O, constructed with compile-time defaults for every backend. The mock provider has a per-request error probability but no way to fail at a chosen instant. The load-test profile has one upstream and no fallback. Gateway and mock both log JSON to stdout. See proposal.md.

## Goals / Non-Goals

**Goals:**

- Transition instants as log records, so detection and recovery are read from timestamps rather than inferred from traffic shape.
- A failure that starts when the experiment says so, not when a coin flip does.
- The two numbers with their formula, from one command.

**Non-Goals:**

- Active health probes; per-backend or per-tenant breaker parameters (a policy concern); retry budgets and idempotency keys toward upstreams; measuring against a networked provider.

## Decisions

1. **A hook on the breaker, called outside the lock.** `Config.OnStateChange(from, to)` fires after every transition — including the lazy open→half-open flip that happens on the next `Allow` or `State` call after the cooldown. Calling it after the mutex is released lets the hook inspect the breaker and keeps the breaker free of I/O; the composition root turns it into a `breaker_state` log line with the backend name captured per breaker.

2. **Configuration through the same environment surface as everything else**, with defaults taken from the breaker package so the two never drift. Validation mirrors the other numeric variables; the minimum-samples-within-window rule catches the one combination that would silently never open.

3. **A failure window on the mock, opened by an admin call.** A duration-based window (`for=<duration>`) is self-clearing, so a run that aborts leaves the mock healthy; the mock logs `from` and `until` at nanosecond precision, and those instants — not the client's clock — are what detection and recovery are measured against. Everything runs on one host, so the mock's and the gateway's clocks agree. Admin endpoints are unauthenticated because the mock is never published.

4. **Three tenants under their limits, one requested alias, fallback present.** With a healthy fallback the client must never see a failure: the experiment measures the breaker, not the outage. Per-tenant rate (8 rps) stays under the sustained limit (10 rps) so the limiter never interferes; three tenants give 24 rps, 12 per replica.

5. **The detection formula is stated before the run.** With window W = 20 filled by the warm-up and threshold θ = 0.5, the breaker opens on the ⌊W·θ⌋ + 1 = 11th consecutive failure (the minimum-samples gate is already satisfied); at r = 12 rps per replica that is 11/12 s plus at most one inter-arrival gap: detection ∈ [0.83, 0.92] s. Recovery is one cooldown (10 s) after opening, plus one inter-arrival until the first probe — measured from the end of the failure window, ≈ 5.9 s for a 5 s window. A 15 s window makes the first probe fail once (half-open → open) and delays closing by a second cooldown.

6. **Verification from three log sources**: the mock's window, each replica's transitions, each replica's cost events (`attempts == 2` are the requests that paid the primary before the breaker opened; `served_provider == "backup"` the ones the fallback carried; `error_code == "upstream_error"` must be zero).

## Risks / Trade-offs

- **Zero-latency mock is the optimistic floor.** Against a real provider the 11 failing attempts each cost a timeout, so detection is dominated by the upstream timeout, not by the breaker. Stated in the measurements doc.
- **Lazy half-open.** The breaker only notices the cooldown has elapsed on the next call; under steady traffic that is one inter-arrival, under no traffic it is indefinite (the design doc's case for active probes).
- **Admin endpoints on the mock.** Harmless as long as the mock is never exposed; the README says so.
