## 1. Namespace

- [ ] 1.1 Derive the namespace from the tenant, route model, reported model (route model when empty), system prompt, temperature, `max_tokens`, fixed markers and version `v2`, length-prefixing each component; verify with `go test ./internal/semcache -run TestNamespace`.
- [ ] 1.2 Pass `max_tokens` from the cache shape and the reported model from the buffered response and from validated stream chunks; verify with gateway tests that real shadow evaluations find no neighbor across different `max_tokens` or reported models, and find one when an unreported model meets the route's model.
- [ ] 1.3 Drop each new component from the derivation once and confirm a test fails for every mutant.

## 2. Documentation

- [ ] 2.1 Describe the header, the body's `model` and the namespace components in `docs/gateway.md` and `docs/design.md`; verify with `make lint-docs`.

## 3. Verification

- [ ] 3.1 Run `make test`, `make lint` and `openspec validate --all` with zero failures.
