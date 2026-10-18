# Design: add-semantic-cache

## Context

The completions flow already has the post-response finalizer hook (`OnFinalize`, run exactly once by accounting on buffered, stream-end, and panic paths), the streamed-usage scanner, authenticated upstreams, and the drop-and-count discipline in the telemetry emitter. docs/design.md §5.4 fixes the danger model (false positives) and the honest metrics. See proposal.md.

## Goals / Non-Goals

**Goals:**

- A measurement instrument whose numbers can be trusted precisely because it cannot affect what it measures.
- The embedder and index seams a serving mode would reuse unchanged.

**Non-Goals:**

- Serving from the cache (`serve` mode does not exist); cross-replica index sharing or persistence; PII-based non-cacheability (a policy-filter concern); TTL/staleness policy; a real tokenizer for anything.

## Decisions

1. **Async-everything shadow.** The hot path does exactly one thing: if mode=shadow and the request is eligible, enqueue {namespace, prompt, response fingerprint, request ID} from a finalizer. The worker does embed → KNN → record → insert. Synchronous shadow would tax every request with embedder latency while returning nothing for it — the worst point in the design space. Consequence accepted: shadow results are their own `cache_shadow` slog events correlated by request ID, not cost-event fields.

2. **Record best similarity always.** τ (`PUSAQ_CACHE_TAU`) only labels events; the similarity, neighbor ID, and both response fingerprints ride on every record. Offline, any τ's precision/recall falls out of the event stream — offline calibration reads exactly this.

3. **Response fingerprints as the production FP signal.** SHA-256 (truncated) of the response content — buffered: the choices' content; streamed: the delta contents the usage scanner already walks. Identical fingerprints on a would-be hit = certainly-correct; different = candidate FP for offline review. Ground truth comes from the labeled calibration set, not from fingerprints alone (textual variation ≠ semantic error) — both stated.

4. **Namespace = SHA-256(tenant | served model | system-prompt hash | "v0" constants).** Exhaustive per constitution rule 2 over the fields that exist; constants hold the places of tools/format/temperature/policy-version so the derivation extends mechanically. Language is content, not config: it lives in the embedding space (cross-language pairs score low), documented rather than hacked in with detection heuristics.

5. **In-process flat index, per replica.** Normalized vectors, cosine = dot product, RWMutex, LRU ring at `PUSAQ_CACHE_MAX_ENTRIES`. Exact KNN, zero dependencies, no hot-path Redis scans, no RediSearch requirement (redis:7 = Memorystore parity doesn't ship it). Cost — per-replica measurement, no restart persistence — is acceptable for shadow and stated in the spec.

6. **Embedder seam mirrors the provider seam.** `embed.Embedder` with two implementations: `Local` — char-trigram feature hashing into 256 dims, L2-normalized: deterministic, $0, offline (weak semantics, stated: it measures the *machinery*; real semantic quality needs the API embedder); `HTTP` — OpenAI-compatible `POST /embeddings` on a configured upstream, reusing the bearer-key and bounded-client patterns. Selection: `PUSAQ_EMBEDDER=local|openai:<upstream>` + `PUSAQ_EMBED_MODEL`. Budgeted calls; failures skip-and-count.

7. **Concurrency budget: fixed worker pool of 8 with a bounded queue**, drop-and-count on overflow — same failure philosophy as telemetry (a lost measurement is acceptable; a delayed user request is not).

## Risks / Trade-offs

- **Local embedder inflates lexical similarity and misses paraphrases** — fine for testing the instrument, wrong for policy decisions; the README says which embedder produced any published number.
- **Per-replica indexes undercount would-be hits** under load balancing. Accepted for shadow; noted as a bias direction (undercount, never overcount).
- **Unbounded namespace cardinality** (one index partition per namespace) is capped implicitly by the global LRU cap.
- **Shadow evaluation of streams waits for stream end** (fingerprint needs the content) — so shadow lag on long streams; irrelevant to correctness, noted.
