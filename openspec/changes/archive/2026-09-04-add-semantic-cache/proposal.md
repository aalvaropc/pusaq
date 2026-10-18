# Proposal: add-semantic-cache

## Why

The semantic cache is where real LLM cost savings live — and where a gateway can start lying: a semantic cache can serve a confidently wrong answer, which an exact cache never does. The constitution (rule 3) therefore mandates shadow mode first: measure what the cache *would* do — hit rate, similarity distribution, embedding toll, candidate false positives — against real traffic before a single cached answer reaches a user. This change builds that measurement instrument.

## What Changes

- **Shadow-mode semantic cache** (`PUSAQ_CACHE_MODE=shadow`, default `off`): eligible completions are evaluated asynchronously — embed the prompt, nearest-neighbor within the request's namespace, record a `cache_shadow` event, insert the new entry. Nothing is ever served from the cache; there is no `serve` mode.
- **Evaluation never perturbs the hot path**: the whole evaluation runs off-request under a bounded concurrency budget; saturation drops evaluations and counts the drops (the telemetry-emitter discipline). Embedder latency is recorded per evaluation so the embedding toll is a measured number.
- **Every event carries `best_similarity`** plus the neighbor's ID and both response fingerprints — precision/recall for any τ is computable offline from the event stream alone; `PUSAQ_CACHE_TAU` (default 0.95) only labels events hit/miss for at-a-glance reading.
- **Exhaustive namespace** (constitution rule 2): tenant | served physical model | system-prompt hash | (tools, response format, temperature, policy version — fixed markers, which the request subset does not carry). Language is content-derived and lives in the embedding space, documented as such.
- **Eligibility by construction**: exactly one user message (optional system messages ahead of it); multi-turn is never evaluated. Streams are eligible (fingerprint from scanned chunks).
- **Embedder as a seam**: `local` (deterministic char-trigram hashing, $0, offline) or `openai:<upstream>` (OpenAI-compatible `/embeddings` with the upstream's bearer key), selected by `PUSAQ_EMBEDDER`.
- **Per-replica in-process index**: flat cosine KNN, LRU-capped (`PUSAQ_CACHE_MAX_ENTRIES`) — exact, dependency-free; per-replica measurement and no restart persistence, stated openly. A serving mode would choose the shared-store design with this data in hand.

## Capabilities

### New Capabilities

- `semantic-cache`: shadow-mode semantic cache measurement — eligibility, namespace isolation, non-perturbing asynchronous evaluation, and the shadow record contract.

## Impact

- New code: `internal/embed`, `internal/semcache`.
- Modified: `internal/gateway/completions.go` (post-response enqueue via the existing finalizer hook), `internal/config`, `cmd/pusaq`, docker-compose (shadow on, local embedder), README.
- No new dependencies.
