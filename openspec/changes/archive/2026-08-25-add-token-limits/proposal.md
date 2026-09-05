# Proposal: add-token-limits

## Why

RPM protects infrastructure; invoices and provider quotas are measured in **tokens per minute**. A tenant sending 10 huge-context requests a minute costs more than one sending 1,000 short ones. The non-trivial part — the reason this is its own change — is that output tokens are unknown until the response ends: the design is reserve-on-admission, reconcile-on-completion (docs/design.md §5.3), and the reconciliation must hold on success, error, panic, and at stream end.

## What Changes

- Per-tenant TPM token bucket alongside RPM: after model resolution (constitution rule 4 — never before, and cache hits will never pay it), the request charges its **input estimate plus an output reservation** (`max_tokens` when the client sets it, a configured default otherwise) against the TPM bucket. Denials share the `rate_limited` (429) contract with a computed retry.
- **Reconciliation on completion**: when actual usage is known, the difference between charged and actual is returned to (or additionally charged from) the bucket by a second atomic Lua script. Reconciliation is guaranteed by a new envelope finalizer hook that the accounting stage runs exactly once — on the buffered defer path, at stream end, and on panic alike.
- Missing usage (e.g. a stream that never delivered its usage chunk) reconciles nothing: the full charge stands — conservative and documented.
- Input tokens are estimated as chars/4 until a real tokenizer earns its place; reserved and actual are both recorded on the cost event so the estimate's error is measurable.
- Config: `PUSAQ_TPM_LIMIT` (tokens/minute, default 100000), `PUSAQ_TPM_BURST` (default = limit), `PUSAQ_TPM_RESERVE_DEFAULT` (output reservation when `max_tokens` is absent, default 512). TPM shares RPM's enablement: active only with `PUSAQ_REDIS_ADDR` set.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `rate-limiting`: adds token-based limits — reservation on admission after model resolution, reconciliation on completion, shared denial contract.
- `cost-telemetry`: completion events additionally record the tokens charged (reserved) so reservation error is observable.

## Impact

- Modified: `internal/ratelimit` (reconcile script + `Adjust`), `internal/core` (envelope finalizers, `TokensCharged` on events), `internal/gateway` (accounting runs finalizers; completions handler charges and registers reconciliation), `internal/config`, `cmd/pusaq`, docker-compose (TPM env), README.
- No new dependencies.
