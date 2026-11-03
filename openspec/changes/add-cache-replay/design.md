## Context

In shadow mode the gateway embeds each eligible prompt, searches the nearest neighbor in its namespace, records a `cache_shadow` line and inserts the prompt, all after the provider answered (`internal/semcache`). Eligibility combines structural conditions with the content rules of `internal/policy`. `cachecal` sweeps τ over labeled pairs and certifies τ\* by a Clopper–Pearson lower bound on precision. The event report pairs the shadow's predicted hit-rate with a false-positive upper bound from response fingerprints. None of these replays a traffic stream against labels, prices it, or compares latency with and without a cache.

## Goals / Non-Goals

**Goals:** one local command that answers "what would the semantic cache have done on this traffic" with the hit-rate always next to its false-positive rate, net savings after the embedding toll, wrong-hit spend apart, latency p50/p95 with and without the cache, and the break-even hit-rate; the same code the gateway runs for shadow evaluation; deterministic numbers for a seed.

**Non-Goals:** serving from the cache, choosing τ (calibration does that), a production traffic sample, an HTTP embedder run, a confidence bound on replay precision, and any change to the gateway's request path, events or configuration.

## Decisions

- **Same evaluation code.** Every request uses one `semcache.Key` namespace (one tenant, one route, no system prompt, temperature and `max_tokens` absent, so `none`). Eligibility applies the content rules of `internal/policy` in the gateway's order — sensitive, injection, temporal — to the single user message; the structural conditions (one user message, no explicit temperature, tenant not opted out) hold by construction of the replayed requests. Eligible requests go through `semcache.Shadow.Evaluate`, the cycle the asynchronous workers run: embed, nearest neighbor, record, insert. A request is a predicted hit at τ when `Neighbor.Hit(τ)` holds, the rule that labels `shadow_hit`.
- **Insertion after evaluation, always.** As in the shadow, every eligible request is inserted after its own lookup, hit or miss. The index therefore evolves independently of τ, so one pass yields the outcome of every τ in the sweep.
- **Labeled answers as fingerprints.** Each sample's fingerprint is the fingerprint of its answer label (intent and language). A predicted hit is false when the neighbor's fingerprint differs from the request's: the evidence the shadow records carry, made exact by the labels. A neighbor that is the same intent in the other language is a false hit: language is not a namespace component, and the cached answer would be in the wrong language.
- **Traffic model.** A seeded permutation ranks the corpus' answers; rank k is drawn with probability proportional to k^−s (s ≥ 0, s = 0 uniform) through an inverse-CDF table; the paraphrase is uniform within the answer. Provider latency per request comes from `mockprovider.Options.SampleLatency` on a separately seeded stream, so latency flags do not change the request sequence.
- **Money.** A request's provider cost is the `pricing.Table` cost of the gateway's input-token estimate (⌈(len(role) + len(content)) / 4⌉ bytes) plus the configured output tokens at the configured chat prices. The embedding toll is the estimate of the embedded text at the configured embedding price, charged on every eligible request. Net savings = provider cost avoided by correct hits − toll. The provider cost that false hits skip is reported apart, never as savings. Break-even hit-rate = toll ÷ provider cost of eligible requests: the correct-hit rate at which net savings reach zero when hits cost what the average eligible request costs. The in-process index has no infrastructure cost to model. Prices are flags; no physical model name appears.
- **Latency.** Without the cache, a request takes its drawn provider latency. With the cache, an ineligible request takes the provider latency, an eligible hit takes its measured embed and search time, and an eligible miss takes embed + search + provider. Percentiles are nearest-rank (`report.Percentile`), the event report's method.
- **Rates.** Hit-rate = predicted hits ÷ eligible requests; false-positive rate = false hits ÷ predicted hits — the event report's definitions. No confidence bound is computed: replayed hits repeat the same pairs and are not independent trials; certification stays with `cachecal`.
- **Outputs.** `cmd/cachereplay` writes `sweep.csv` (one row per τ, hit-rate next to false-positive rate), `requests.csv` (one row per request) and `summary.md` under `out/replay/`; undefined ratios are written `undefined`.

## Risks / Trade-offs

- The corpus is synthetic and written by one author; its paraphrases and traps define the numbers. The replay states this next to every result.
- The local embedder is lexical: its hit-rate and false-positive rate measure trigram overlap, not paraphrase quality, and its microsecond embed time is not the latency of an HTTP embedder.
- Provider latencies come from the mock's two-point model and prices from flags, so dollars and milliseconds describe those inputs, not a provider.
- The flat index scans every entry of the namespace, so a long replay costs O(N²) similarity computations; the default request count keeps a run within seconds.
