# Design: add-token-limits

## Context

RPM limiting exists (atomic Lua bucket, fail-open, 429 contract). Usage is already captured on the envelope for buffered and streamed responses. The accounting stage already owns the exactly-once finalization moment — which is precisely when reconciliation must run. See proposal.md.

## Goals / Non-Goals

**Goals:**

- The reserve→reconcile cycle from docs/design.md §5.3, atomic on both ends, guaranteed on every outcome including stream end.
- TPM in its permanent position: after model resolution, inside the completions flow (constitution rule 4).

**Non-Goals:**

- Real tokenization (chars/4 estimate until a tokenizer earns its dependency); budget-in-dollars cutoffs; per-tenant limit overrides; measuring max overrun (measured in a load run, not here).

## Decisions

1. **Envelope finalizers, run by Accounting.** `core.Request` gains `OnFinalize(fn)`; the accounting stage runs registered finalizers exactly once at its emission moment — the buffered defer, the stream-end finalize, and the panic path are already unified there, so reconciliation inherits the exactly-once guarantee instead of reimplementing it. Finalizers run before the event is emitted, so what they record lands on the event. Alternative (defer inside the completions handler) rejected: it fires at handler return, which is before a stream ends.

2. **TPM check lives in the completions flow, not a pipeline stage.** Constitution rule 4: TPM after model selection; a generic stage runs before the handler resolves the model. The handler charges after route resolution and before the provider call, via the same `Admitter`-style port (`TokenLimiter`: `Allow` + `Adjust`); nil disables. When routing later moves into its own stage, the TPM call moves with it.

3. **Reconcile is a second small Lua script** (`Adjust`): refill by elapsed time, then `tokens = min(B, tokens + delta)`, persist with TTL. Refunds cap at capacity; extra charges (actual > reserved) go negative naturally, pushing the next refill window out — the design doc's semantics. Fire-and-forget with the same bounded budget as admission; a failed refund under Redis outage is accepted (fail-open symmetry).

4. **Charge composition:** `input_estimate (ceil(chars/4) over message contents) + output reservation (max_tokens | PUSAQ_TPM_RESERVE_DEFAULT)`. Both halves recorded as `TokensCharged` on the event next to actual usage — publishing the estimate's error is the honest move and feeds the calibration a measurement run needs.

5. **Missing usage keeps the charge.** A stream that never delivered usage reconciles nothing — conservative by design: refunding on unknown actuals would let a client zero out its charges by aborting streams.

## Risks / Trade-offs

- **chars/4 is a crude input estimate.** Accepted and measurable (charged vs actual on every event); a tokenizer is a later, earned dependency.
- **Reservation pessimism throttles high-`max_tokens` tenants.** Accepted: the design doc's percentile-based estimate needs traffic history that does not exist yet.
- **Failed refunds under Redis outage leak charge until bucket TTL.** Accepted: bounded by TTL, symmetric with fail-open, logged.
