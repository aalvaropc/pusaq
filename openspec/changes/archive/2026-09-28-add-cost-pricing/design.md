# Design: add-cost-pricing

## Context

Cost events already carry the served model and provider-reported usage, and the accounting stage runs request finalizers exactly once on every path — buffered return, stream end, panic — before it emits the event. Configuration is environment-only. See proposal.md.

## Goals / Non-Goals

**Goals:**

- Dollars on every priced completion, computed once, from the usage the provider reported, keyed by the model that served it.
- A version label on every priced event, so a price change never silently rewrites history.
- Configuration mistakes (unpriced route model, table without version) fail at startup, not at report time.

**Non-Goals:**

- Budgets or cutoffs in dollars (a policy concern); pricing the embedding toll of the shadow cache; per-tenant price overrides; currency conversion.

## Decisions

1. **A finalizer, not a handler-return computation.** Usage lands on the request after a buffered return or at the end of a stream; the accounting stage already runs finalizers at exactly that moment, before building the event. Pricing in a finalizer therefore covers both paths with one code path and inherits the exactly-once guarantee. An error before the provider answered leaves usage empty, and the finalizer prices nothing.

2. **Price keyed by served model, not by backend.** The fallback chain preserves the model contract, so the model — not the backend that happened to answer — determines the rate. Recorded next to `served_provider`, the event still says who served it.

3. **Nano-dollar rounding.** `(prompt·in + completion·out)/1e6` rounded to 1e-9 USD removes binary float noise (a $0.10-per-million model would otherwise print 5.600000000000001e-06) without losing anything real: one token of the cheapest plausible rate is 1e-7.

4. **Rates in USD per million tokens.** The unit every provider publishes; parsing `model=input/output` keeps a table a one-line environment variable, consistent with `PUSAQ_ROUTES` and `PUSAQ_UPSTREAMS`.

5. **Cross-validation with routes at startup.** A table that misses a route's model would produce events silently lacking cost for that model — the failure mode that corrupts a report. Refusing to start turns it into a configuration error with the model's name in it.

6. **`cost` as a nested object with `omitzero`.** Mirrors `usage`: a priced $0 (all rates zero) still carries the version label, so the object is non-zero and rendered; an unpriced event omits it entirely. The emitter flattens it to `cost_usd` and `price_table_version` for log consumers.

## Risks / Trade-offs

- **Provider-reported usage is the source of truth.** If a provider omits or misreports usage, the cost is absent or wrong; the event still carries `tokens_charged` from the limiter as a cross-check.
- **One global table.** Per-tenant price overrides are out of scope for this change.
