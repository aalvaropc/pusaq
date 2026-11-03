## Context

The breaker (`internal/breaker`) is pure state that callers drive with `Allow`, `Record` and `Release`. In half-open, `record` closes or reopens on any outcome, and `Release` decrements the probe count whenever the breaker is half-open, whatever the attempt was. A slow request admitted while the breaker was closed can therefore close it while the real probe is still in flight.

The telemetry emitter checks an atomic `closed` flag and then sends on a buffered channel. `Close` sets the flag, closes `quit`, and the drain goroutine flushes until the buffer is empty. An `Emit` that passed the flag check before `Close` can send after the final flush: the event is neither written nor counted (1 of 1,512 runs of 16 emitters × 50 events in the reproduction). The log sink writes `attempts` only next to `served_provider`. The token-charge fail-open path only logs; the request limit and the budget read do the same, so none of the three is visible in the event. `CostEvent.Latency` is a `time.Duration` tagged `latency_ms`, so a JSON encoding of the struct writes nanoseconds while the sink writes milliseconds.

The buffered provider client uses `http.DefaultTransport` (two idle connections per host); the streaming client clones it and keeps that limit; the embedding client uses it directly.

`calibrate.Sweep` stores undefined precision as 0 and qualifies a τ whose point precision meets the floor, whatever its support.

## Goals / Non-Goals

**Goals:**
- Half-open decided only by its own probes; probe slots freed only by its own probes.
- No event lost silently around `Close`; every event carries `attempts`; fail-open admissions visible per event; one latency unit in every encoding.
- Connection reuse at the gateway's concurrency against one upstream.
- τ* certified only with statistical support; undefined precision visible as undefined.

**Non-Goals:**
- New environment variables, breaker parameters, or changes to fallback order.
- Changes to the semantic cache namespace or to shadow-mode evaluation.
- A larger calibration dataset: the seed set stays a harness check.

## Decisions

1. **Breaker tickets with a generation counter.** The breaker increments a generation each time it opens or closes; the half-open period after an open keeps the open's generation, because an open breaker admits nothing. `Allow` returns a `Ticket` holding the breaker and the generation that admitted it; `Ticket.Record` and `Ticket.Release` act only when the breaker is still in that generation. In half-open every ticket of the current generation is a probe, so a current ticket decides the state and frees its slot, and any other ticket is ignored. The same rule keeps attempts admitted before an open out of the fresh closed window that follows recovery. A zero `Ticket` is a no-op, so the gateway needs no nil checks for unguarded backends. Alternative considered: a per-ticket probe flag plus a half-open episode counter — two fields to express what one generation already identifies.

2. **Emitter read/write lock.** `Emit` holds a read lock across the closed check and the non-blocking send; `Close` takes the write lock to set the flag before closing `quit`. Once `Close` holds the write lock, no send is in progress and every subsequent `Emit` sees the flag, so the final flush drains everything that was sent. The hot path pays an uncontended read lock; the sink is still never waited on. Alternative considered: counting the race as a drop — it would keep the loss, only counted.

3. **`fail_open` list.** A request field collects the store-backed decisions that failed open, `rpm`, `tpm` and `budget`, named after the configuration that enables them; the accounting stage copies it into the event and the log sink writes it comma-joined, like `policy_flags`. The existing warning logs stay. The request limit and the budget read get the same marking as the token charge so the field means one thing for all three.

4. **Latency as integer milliseconds.** `CostEvent.LatencyMS int64` replaces `Latency time.Duration`; the stages fill it with `time.Since(start).Milliseconds()`, the value the sink already wrote. `attempts` loses `omitempty` and the sink writes it on every event.

5. **Pooled transports, fixed size.** Each client clones `http.DefaultTransport` and raises `MaxIdleConns` and `MaxIdleConnsPerHost` to 256; each transport talks to one host, so the two limits are the same pool. 256 sits above the deployed per-instance request concurrency (200), so a replica at full concurrency against one upstream returns every connection to the pool; idle connections still close after the default 90 s. Dial, TLS, response-header and client timeouts and the redirect policy are unchanged. No environment variable: the limit only bounds idle memory, and connections above it close instead of failing.

6. **Clopper–Pearson lower bound for τ\*.** τ\* = min{τ : L(τ) ≥ floor}, where L is the one-sided exact (Clopper–Pearson) lower confidence bound on precision from true hits out of predicted hits, at confidence `-confidence` (default 0.95). It is computed by bisection on the binomial upper tail, which is monotone in p; with no false hit it equals (1 − confidence)^(1/hits). Clopper–Pearson guarantees at least the stated coverage for any sample size and any true precision, while the Wilson interval's coverage oscillates and can fall below nominal, notably near 1; that matches the rule that the cache never claims more precision than the data supports. A minimum-hits threshold alone was rejected: it ignores false hits, and the exact bound already implies one (299 predicted hits with no false hit for 0.99 at 95%). Confidence is validated to [0.5, 1): below 0.5 a lower bound sits above the point estimate.

7. **Undefined is not zero.** Precision with no predicted hit, recall with no equivalent pair and F1 with neither are NaN in memory and `undefined` in the CSV. F1 is 2·TP / (2·TP + FP + FN), defined whenever a pair is labeled equivalent or predicted a hit. The CSV adds `hits`, `false_hits` and `precision_lower` columns so support is visible next to every point.

## Risks / Trade-offs

- The bound holds per τ. Taking the smallest qualifying τ of the 40-point sweep is a selection, so the confidence is per point, not family-wise; the bound also describes the labeled dataset, not production traffic. Both limits are stated in the docs.
- Ignoring stragglers discards real outcomes: a probe that ends after another probe closed the breaker no longer counts in the new window. The breaker reacts to attempts admitted in its current state only.
- The calibration CSV changes columns; a consumer of the old four-column format breaks.
- Larger idle pools hold up to 256 idle connections per transport after a burst, until the idle timeout closes them.
