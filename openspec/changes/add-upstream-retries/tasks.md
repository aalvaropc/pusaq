## 1. Provider

- [ ] 1.1 Wrap transport failures before a response and statuses 408, 429, 500, 502, 503, 504 in `RetryableError`, with `Retry-After` parsed as delta-seconds or an HTTP date.
- [ ] 1.2 Send `Idempotency-Key` from `ChatRequest.IdempotencyKey` on buffered and streamed calls, outside the body.
- [ ] 1.3 Tests: classification per status and transport failure, `Retry-After` forms, header on both paths, body unchanged.

## 2. Retry budget and backoff

- [ ] 2.1 `internal/retry`: token-bucket budget (ratio per request, reserve 10, refill one per second, refund) with an injected clock; full-jitter backoff with a 200 ms base and a 2 s cap.
- [ ] 2.2 Tests: credit, withdraw, refund, floor refill, reserve cap, stated bound over a run; backoff ceiling per retry and cap.

## 3. Gateway

- [ ] 3.1 Retry the same backend in the chain walker within the per-request maximum, the window and the budget; ask the breaker per attempt; refund a withdrawal not spent; derive the idempotency key from the request ID.
- [ ] 3.2 Record `retries` and `retry_denied` on the request and on every cost event, buffered and streamed; write both in the log sink.
- [ ] 3.3 Tests with injected clock, wait and jitter: each retryable class, no retry on rejections, other failures or cancellation, per-request maximum, `Retry-After` within and beyond the window, budget exhaustion with attempts per request measured with and without the budget, breaker tickets per attempt, no retry after a stream started, one key on every attempt and fallback, one token charge and one reconciliation, event fields.

## 4. Configuration

- [ ] 4.1 `PUSAQ_RETRY_MAX`, `PUSAQ_RETRY_BUDGET_RATIO`, `PUSAQ_RETRY_WINDOW`, validated finite and bounded; wire the budget in `cmd/pusaq` and state the settings in the startup log.
- [ ] 4.2 Tests: defaults, valid values, rejected values.

## 5. Docs and verification

- [ ] 5.1 Update `docs/gateway.md`, `docs/design.md` and `docs/configuration.md`.
- [ ] 5.2 Mutation check of the core rules; record the mutants and the tests that kill them.
- [ ] 5.3 Run `make breakertest` with default configuration and compare with the published numbers.
- [ ] 5.4 `make test`, `make lint` and `openspec validate --all` pass.
