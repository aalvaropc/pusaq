# Design: add-tpm-overrun-measurement

## Context

Token limiting charges `ceil(chars/4) + max_tokens` (or the configured default) on admission and reconciles the difference to actual usage in a finalizer. The load-test profile turns token limiting off to isolate the request limiter's toll, and its mock answers instantly, so nothing is ever in flight. The mock's usage is fixed at 23 tokens per completion. See proposal.md.

## Goals / Non-Goals

**Goals:**

- The number the design promises: how far consumption can exceed the bucket before reconciliation catches up, with the formula that bounds it stated before the run.
- Reuse of the existing profile and harness; the limiter and breaker runs keep their defaults and their numbers.

**Non-Goals:**

- A tokenizer, a usage knob on the mock, or an output estimate from history — the two `max_tokens` arms on a fixed usage exercise both signs of the reservation error without any of them.

## Decisions

1. **Emulated latency.** With an instant upstream the reconciliation lands before the next arrival and the overrun is one request's error. The run sets the mock to sample [1 s, 2 s] per request so tens of requests overlap; the profile's default stays 0 so the other runs are untouched. A zero-latency control run is part of the protocol.

2. **Two arms by `max_tokens`, one fixed usage.** `max_tokens: 8` charges 10 and consumes 23 (under-reservation, error −13); `max_tokens: 64` charges 66 and consumes 23 (over-reservation, +43). Two tenants and two aliases keep the arms separate in the limiter, the mock's counter, and the events.

3. **Bound in tokens, computed from arrival times.** Over the window: `Σactual ≤ B + r·(T + ε)`. At any instant: the running sum of consumed tokens minus `B + r·(t − t0)`. Its peak in the under-reserving arm is bounded by the burst's worth of under-reserved admissions, `B × (actual − charged) / charged` (1 300 tokens at the defaults); reconciliation arriving during the burst keeps the observed value below it. The over-reserving arm can never exceed the bound: what it consumes is always less than what it charged.

4. **Events as the source.** Every cost event carries `tokens_charged`, the actual usage, the arrival timestamp, and the latency, so charged vs consumed, the peak, and the number in flight all come from the log — the same vantage point the limiter runs use — cross-checked against k6's counts and the mock's per-model counter.

5. **Empty `PUSAQ_TPM_BURST` means default.** The Compose anchor declares the variable from a knob that is empty for the other runs; treating empty as unset follows `PUSAQ_REDIS_ADDR` and `PUSAQ_PRICES`.

6. **Request limiting raised, not disabled**, for this run (6 000/min, burst 1 000): the request limiter stays in the path as in production, and 20 rps per arm never trips it.

## Risks / Trade-offs

- **k6 VU pressure at 2 s latency**: 20 rps × 2 s ≈ 40 in flight per arm; `maxVUs` 300 covers it.
- **Millisecond timestamps** in the log bound the arithmetic error to a fraction of a token.
- **The bucket goes negative** in the under-reserving arm (reconciliation has no floor): the debt is repaid by refill before anything else is admitted, which is exactly what keeps the window total within the bound.
