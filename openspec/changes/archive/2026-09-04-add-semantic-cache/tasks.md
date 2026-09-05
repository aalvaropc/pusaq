# Tasks: add-semantic-cache

## 1. Embedder seam

- [x] 1.1 `internal/embed`: `Embedder` interface; `Local` char-trigram hashing (dim 256, L2-normalized); tests: determinism, normalization, identical texts → 1.0, disjoint texts ≪ overlapping texts.
- [x] 1.2 `internal/embed`: `HTTP` embedder for OpenAI-compatible `/embeddings` (bearer when keyed, bounded client, normalized errors); httptest tests: request shape (`model`, `input`), auth header present/absent, upstream failure surfaces as error.

## 2. Index and shadow worker

- [x] 2.1 `internal/semcache`: flat cosine `Index` with namespace partitions and global LRU cap; tests: KNN correctness on crafted vectors, namespace isolation, LRU eviction at cap, race-safety under `-race`.
- [x] 2.2 `internal/semcache`: `Shadow` worker — bounded queue + fixed workers, drop counter; evaluate = embed → KNN → emit `cache_shadow` slog record (request ID, namespace, best similarity, neighbor ID, both fingerprints, embedder latency, hit-vs-τ) → insert; embedder failure skips and counts. Tests with a captured slog handler: hit and miss records, first-entry (no neighbor) inserts silently, saturation drops counted, worker drains on Close.
- [x] 2.3 Namespace fingerprint builder (tenant | served model | system-prompt hash | v0 constants) + response fingerprinting (buffered content; streamed scanned deltas); unit tests incl. different-tenant / different-system-prompt isolation.

## 3. Gateway integration

- [x] 3.1 `internal/gateway/completions.go`: eligibility check (exactly one user message, optional leading system messages); on success enqueue via `OnFinalize` (buffered response bytes; streams after scan) — hot path does only the enqueue; mode=off short-circuits. Tests: eligible buffered + streamed enqueue with correct namespace/fingerprint, multi-turn never enqueues, off mode silent, failed requests never insert.

## 4. Config, wiring, environment

- [x] 4.1 `internal/config`: `PUSAQ_CACHE_MODE` (off|shadow), `PUSAQ_CACHE_TAU` ((0,1], default 0.95), `PUSAQ_CACHE_MAX_ENTRIES` (>0, default 10000), `PUSAQ_EMBEDDER` (`local` or `openai:<upstream>` naming a configured upstream), `PUSAQ_EMBED_MODEL`; validation + tests.
- [x] 4.2 `cmd/pusaq`: build embedder + shadow worker when mode=shadow (startup log), close worker in the shutdown sequence; compose enables shadow with the local embedder; README section (shadow-first framing, τ by precision, which embedder produced which number).

## 5. Verification and delivery

- [x] 5.1 Full suite `-race` green; lint + vet clean.
- [x] 5.2 E2E (local embedder, shadow on): near-identical prompts → second request logs `cache_shadow` with `shadow_hit:true` and high similarity; disjoint prompt → recorded miss; mode=off → zero cache events.
- [x] 5.3 Conventional commit(s) on `feat/semantic-cache`, push, PR to main; CI green.
