# Tasks: fix-upstream-error-classification

## 1. Adapter

- [ ] 1.1 Status classification (400/413/422 → `invalid_request`, others → `upstream_error`) for buffered and streamed calls; tests without body leaks.
- [ ] 1.2 Oversized 200, 200 with an `error` object, 200 without choices → `upstream_error`; tests.

## 2. Chain and breaker

- [ ] 2.1 `Breaker.Release`; test that a released probe slot is reusable.
- [ ] 2.2 One generic chain walker for buffered and streamed calls; cancellation stops the chain with `client_canceled` and no breaker outcome.
- [ ] 2.3 Gateway tests: provider rejections never reach the fallback or trip a breaker; canceled requests leave breakers closed.

## 3. Docs and delivery

- [ ] 3.1 `docs/design.md` §4 and §5.5, `docs/gateway.md` errors.
- [ ] 3.2 Suite with `-race`, lint, `openspec validate --all`; draft PR.
