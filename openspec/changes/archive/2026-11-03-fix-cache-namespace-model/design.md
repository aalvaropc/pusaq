## Context

See proposal.md - Why. The order of the shadow cycle decides which inputs the namespace can use:

1. The completions handler registers a request finalizer once the provider has answered: after the buffered response is built, or after the stream scanner is created.
2. The finalizer runs at the end of the request (buffered return, or stream end in the accounting stage). It offers a sample only when the answer completed: always for a buffered success, only after `[DONE]` for a stream. The namespace is derived here.
3. A shadow worker then embeds the prompt, searches the nearest neighbor in that namespace (the lookup), records `cache_shadow`, and inserts the entry.

Lookup and insertion both run after the provider's answer is complete, so the model the provider reported is known at both. The `X-Pusaq-Model` header is written before that: for streams, before the first chunk arrives.

## Goals / Non-Goals

**Goals:**

- Answers produced under different `max_tokens` or reported models never neighbor each other.
- The namespace stays anchored in configuration even when a provider reports an arbitrary model name.
- Specs and docs state what the header, the body and the namespace carry.

**Non-Goals:**

- Tools and response-format components beyond the fixed markers: the accepted request subset rejects both fields.
- Changing `X-Pusaq-Model`, the body's `model`, cost-event fields or pricing.
- Serving from the cache.

## Decisions

**Route model plus reported model.** The namespace keeps the route's physical model and adds the provider-reported model; an empty report takes the route's physical model. The reported name is what follows versions behind a floating upstream alias, and the timing above makes it available to both lookup and insertion.
Alternatives: the reported model alone would trust upstream data entirely, so a provider reporting a constant or generic name would merge the answer spaces of distinct configured models; the route model alone (the previous key) cannot follow versions; a configured version field duplicates configuration and goes stale without notice when the upstream alias moves. A lookup that ran before the provider call could use only the route's model; this keying depends on the shadow cycle running after the answer.

**Exact `max_tokens`, `none` when absent.** The provider request omits `max_tokens` when the client did, so absence is the provider default and keys apart from every explicit value.
Alternative: reusing an entry produced under a larger limit needs the entry's finish reason and serving semantics. The exact value costs hit-rate only, never precision.

**Length-prefixed components.** Each component is hashed as its length followed by its bytes. NUL separators were unambiguous while the system prompt was the only free-form component; the reported model is free-form upstream data next to it, so a NUL inside either could move bytes across the boundary.

**Version marker `v2`.** The namespace layout changes, so the marker that versions eligibility rules and derivation moves with it.

**Stream chunks report the model.** The stream scanner keeps the first non-empty `model` of a validated chunk. OpenAI-shaped streams repeat one value per chunk; keeping the first avoids per-chunk work after it is known.

**Language stays a stated limit.** No language signal exists in the request path. The content rules match specific English and Spanish cue phrases; they do not classify a prompt's language, and a prompt without cues carries no signal. A new detector would be an unmeasured heuristic whose mistakes split or merge namespaces without a record. A cross-language would-be hit shows up in shadow records as differing response fingerprints.

**Header stays the route's physical model.** Stream headers are written before any chunk, and errors after resolution carry the header without any provider answer, so the header cannot carry the reported model. The specs and docs change instead of the code.

## Risks / Trade-offs

- [Backends in one fallback chain report different names for the same model] → their answers key apart; hit-rate drops, precision does not.
- [A provider reports an unversioned name for a floating alias] → version changes stay invisible to the namespace; `docs/design.md` states it.
- [Exact `max_tokens` fragments namespaces] → the cost is visible as fewer neighbors in shadow records.
- [Language is not a component] → stated in the spec and docs; differing fingerprints mark such would-be hits.
