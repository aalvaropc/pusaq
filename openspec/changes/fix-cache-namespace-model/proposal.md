## Why

The shadow namespace keys on the route's configured physical model and ignores `max_tokens`. Answers produced under different `max_tokens` share a namespace, so a would-be hit can pair a complete answer with a truncated one; and when the configured upstream name is a floating alias, answers from different model versions share a namespace. The specs also describe `X-Pusaq-Model` as "the upstream model that produced it" and the namespace as using the "served physical model", while the header carries the route's physical model and the response body carries the model the provider reported.

## What Changes

- The namespace includes the request's explicit `max_tokens`; its absence is its own key.
- The namespace includes the model the provider reported in the evaluated response, next to the route's physical model. When the provider reports none, the route's physical model takes its place.
- Namespace components are length-prefixed before hashing, so a provider-reported model or a system prompt cannot shift bytes into a neighboring component.
- The namespace version marker moves from `v1` to `v2`.
- Language stays outside the namespace; the spec and docs state it as a limit.
- The `chat-completions` and `semantic-cache` specs, `docs/gateway.md` and `docs/design.md` describe `X-Pusaq-Model` as the route's physical model and the body's `model` as the provider-reported name.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `semantic-cache`: the namespace adds `max_tokens` and the provider-reported model, encodes its components unambiguously, and states that language is not a component.
- `chat-completions`: served-model reporting distinguishes the header (the route's physical model) from the body's `model` (the provider-reported name).

## Impact

- Code: `internal/semcache/shadow.go` (namespace derivation), `internal/gateway/cache.go` (cache shape), `internal/gateway/completions.go` (namespace inputs, including the `model` of validated stream chunks).
- Shadow records carry namespaces that differ from `v1` ones. The index lives in process and starts empty at every restart, so nothing migrates.
- Responses, headers, cost events, limits and eligibility are unchanged.
