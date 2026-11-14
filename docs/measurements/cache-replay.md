# Measurement: semantic cache replay

**Question.** What would the semantic cache have done on a stream of
traffic: how often would it hit, how many of those hits would serve a wrong
answer, does the provider spend it avoids cover the embedding toll, and what
does it do to latency? The hit-rate is published only next to its
false-positive rate (constitution rule 3), and a false positive can only be
counted against labels, so the replay runs labeled synthetic traffic.

**Answer for this run.** With the lexical `local` embedder, the replay's
hit-rate is almost entirely exact repeats. At τ = 0.95 the hit-rate is
89.24% with a false-positive rate of 0.68%; an exact-match cache on the same
traffic reaches 88.64% with no false hit, and every one of the 30 hits on a
text not seen before is a false hit. Lowering τ adds paraphrase hits only
together with many more trap hits. These numbers describe a synthetic corpus
and a lexical embedder; they are not a production measurement.

## Protocol

1. **Corpus** (`testdata/replay/intents.csv`, below): labeled prompts, each
   carrying its intent, language, family of look-alike intents, and the trap
   kind that separates the family's intents.
2. **Traffic.** A seeded permutation ranks the corpus' 124 answers (intent ×
   language); each request draws a rank with probability proportional to
   rank^−s (Zipf, `-zipf`, s = 0 is uniform) and a paraphrase uniformly within
   that answer. Each request also draws a provider latency from the mock
   provider's latency model (`mockprovider.Options.SampleLatency`: 90% uniform
   in [0.5·p50, 1.5·p50] capped at the maximum, 10% uniform in [p50, maximum]) on a
   separately seeded stream. Same seed and flags, same sequence.
3. **Evaluation**, in request order, with the gateway's own code: one
   `semcache.Key` namespace (one tenant, one route, no system prompt,
   temperature and `max_tokens` absent); the content rules of
   `internal/policy` in the gateway's order (sensitive, injection, temporal)
   decide eligibility, while the structural conditions (one user message, no
   explicit temperature, tenant not opted out) hold by construction; eligible
   requests run `semcache.Shadow.Evaluate`, the cycle the shadow workers run:
   embed, nearest neighbor, record, insert. Every eligible request is inserted
   after its own lookup, hit or miss, so the index evolves independently of τ
   and one pass yields every τ.
4. **Hits.** A request is a predicted hit at τ when `Neighbor.Hit(τ)` holds
   (similarity ≥ τ, the rule that labels `shadow_hit`). Each sample's
   fingerprint is the fingerprint of its answer label; a predicted hit is
   **false** when the neighbor's fingerprint differs. A neighbor that is the
   same intent in the other language is a false hit: language is not a
   namespace component, and the cached answer would be in the wrong language.
5. **Rates.** Hit-rate = predicted hits ÷ eligible requests. False-positive
   rate = false hits ÷ predicted hits. Correct-hit rate = correct hits ÷
   eligible requests. No confidence bound is computed: replayed hits repeat
   the same pairs and are not independent trials; certifying τ is
   `make calibrate`'s job.
6. **Money.** A request's provider cost is the `pricing.Table` cost of the
   gateway's input estimate (⌈(len(role) + len(content)) / 4⌉ tokens) plus
   `-output-tokens` at `-input-price`/`-output-price`. The embedding toll is
   ⌈len(prompt) / 4⌉ tokens at `-embed-price`, on every eligible request.
   **Net savings = provider cost avoided by correct hits − toll.** The
   provider cost that false hits skip is reported apart and never counted as
   savings: each one is a wrong answer served. **Break-even hit-rate** = toll
   ÷ provider cost of eligible requests: the correct-hit rate at which net
   savings reach zero when hits cost what the average eligible request costs.
   The in-process index has no infrastructure cost to subtract.
7. **Latency.** Without the cache, every request takes its drawn provider
   latency. With the cache at τ, an ineligible request takes its provider
   latency, an eligible hit takes its measured embed + search time, and an
   eligible miss takes embed + search + provider. Percentiles are nearest-rank
   (rank ⌈p·n/100⌉ of the ascending sample), the event report's method.

## Corpus

620 prompts: 62 intents, each with five paraphrases in English and five in
Spanish (124 answers), in 32 families. 28 families group two or three
look-alike intents that need different answers; 4 families hold one intent.

| Trap kind | Families | Example intents of one family |
|---|---|---|
| entity | 11 | capital of Peru / capital of Bolivia / currency of Peru; premium / basic plan price; reset password / reset card PIN |
| action | 5 | cancel / renew / upgrade subscription; enable / disable two-factor authentication; send to / receive from Chile |
| negation | 4 | payment methods accepted / not accepted; banks that support / do not support instant transfers |
| date | 4 | invoice total for January / February; 2024 / 2025 tax statement; business hours on weekdays / Sundays |
| quantity | 4 | 100 / 1000 kilometers in miles; fee for transferring 100 / 5000 dollars; paying 5 / 30 days late |
| none | 4 | contact support, free trial, billing address, and the dollar exchange rate |

How it was built: one author wrote a base intent per family in a support and
banking domain, close to the trap kinds of the calibration seed
(`testdata/calibration/seed-pairs.csv`), five paraphrases per language for
it, and then each look-alike intent by changing only the entity, verb,
negation, date or quantity in those same paraphrases, so traps are lexically
close by design. The exchange-rate intent carries a time-dependence cue in
every paraphrase ("current", "today", "right now", "hoy", "ahora mismo"…);
the content rules keep all ten out of the cache, and a test fails if any
other prompt is flagged. The loader rejects duplicate texts, intents in two
families, families with two kinds, single-intent trap families and
multi-intent `none` families.

Limits of the corpus: it is synthetic, small, written by one person in one
domain, and its paraphrases are short questions with uniform frequency
within an intent. Every text is unique, so a repeated text is always the same
answer. With 620 texts, the share of exact repeats is set mostly by the
number of requests, not by any real traffic pattern.

## Environment and parameters

| Item | Value |
|---|---|
| Date | 2026-11-14 |
| Code | commit `358e481` |
| Machine | Intel Core i5-13600K (20 logical CPUs), 32 GB RAM, Fedora Linux, kernel 7.3.3 |
| Go | go1.26.10 (`GOTOOLCHAIN=go1.26.10+auto`) |
| Command | `make replay` (defaults) |
| Traffic | 5000 requests, Zipf exponent 1 over answers, uniform paraphrase, seed 1 |
| Embedder | `local` (character trigrams, lexical) |
| τ | 0.95 (`PUSAQ_CACHE_TAU`'s default); sweep 0.80–0.995 step 0.005 |
| Prices (USD per 1M tokens) | input 0.10 and output 0.40 (the Compose mock's synthetic prices), embedding 0.02 (illustrative) |
| Output tokens per answer | 11 (the mock provider's canned answer) |
| Provider latency | mock model, p50 200 ms, maximum 2 s (`make mock`'s settings) |
| Index caps | 10,000 entries, 64 MiB (gateway defaults; no eviction at 5000 requests) |

## Results (2026-11-14)

**At τ = 0.95.**

| Eligible | Predicted hits | Hit-rate | False hits | False-positive rate | Correct hits | Correct-hit rate |
|---|---|---|---|---|---|---|
| 4965 of 5000 | 4431 | 89.24% | 30 | 0.68% | 4401 | 88.64% |

| Hits on exact repeats | Hits on text not seen before | Correct among them | Exact-match cache on the same traffic |
|---|---|---|---|
| 4401 | 30 | 0 | 4401 hits, 88.64%, no false hit |

35 requests (the exchange-rate intent) were kept out as `temporal`; no
eligible request failed insertion.

**Money (USD, synthetic prices).**

| Provider spend without cache | Embedding toll | Avoided by correct hits | Net savings | Skipped by false hits (not savings) | Break-even correct-hit rate |
|---|---|---|---|---|---|
| 0.027905 | 0.001073 | 0.024577 | 0.023504 | 0.000170 | 3.87% |

**Latency.**

| Arm | p50 | p95 |
|---|---|---|
| Without cache (provider, mock model) | 212.069 ms | 1070.097 ms |
| With cache at τ = 0.95 | 0.701 ms | 224.611 ms |
| Embed + search, eligible requests | 0.587 ms | 3.926 ms |

**τ sweep (selected rows of `out/replay/sweep.csv`).**

| τ | Hits | Hit-rate | False hits | FP rate | Correct-hit rate | Unseen-text hits | Correct among them | Net savings (USD) | p95 with cache |
|---|---|---|---|---|---|---|---|---|---|
| 0.80 | 4606 | 92.77% | 175 | 3.80% | 89.24% | 205 | 30 | 0.023672 | 174.222 ms |
| 0.85 | 4539 | 91.42% | 127 | 2.80% | 88.86% | 138 | 11 | 0.023565 | 197.766 ms |
| 0.90 | 4504 | 90.72% | 99 | 2.20% | 88.72% | 103 | 4 | 0.023526 | 206.270 ms |
| 0.95 | 4431 | 89.24% | 30 | 0.68% | 88.64% | 30 | 0 | 0.023504 | 224.611 ms |
| 0.97 | 4412 | 88.86% | 11 | 0.25% | 88.64% | 11 | 0 | 0.023504 | 227.756 ms |
| 0.99 | 4402 | 88.66% | 1 | 0.02% | 88.64% | 1 | 0 | 0.023504 | 230.436 ms |
| 0.995 | 4401 | 88.64% | 0 | 0.00% | 88.64% | 0 | 0 | 0.023504 | 231.660 ms |

**False hits at τ = 0.95 by trap kind:** quantity 12, negation 9, date 7,
action 2; none across languages and none between unrelated intents. The
most frequent: 1000 → 100 kilometers in miles (EN, 5), 100 → 1000 kilometers
(ES, 3), banks that do not support → support instant transfers (ES, 3), 2024
→ 2025 tax statement (ES, 3).

**Sensitivity (τ = 0.95, other flags at their defaults).**

| Variation | Eligible | Hit-rate | FP rate | Exact-match hit-rate | Correct unseen-text hits | Net savings (USD) | Break-even |
|---|---|---|---|---|---|---|---|
| seed 2 | 4965 | 89.20% | 0.75% (33/4429) | 88.54% | 0 | 0.023421 | 3.83% |
| seed 3 | 4947 | 89.08% | 0.64% (28/4407) | 88.52% | 0 | 0.023362 | 3.86% |
| seed 4 | 4959 | 89.15% | 0.66% (29/4421) | 88.57% | 0 | 0.023295 | 3.76% |
| seed 5 | 4870 | 89.10% | 0.71% (31/4339) | 88.46% | 0 | 0.023083 | 3.93% |
| 1000 requests | 992 | 67.34% | 1.65% (11/668) | 66.23% | 0 | 0.003460 | 3.87% |
| 1000 requests, seed 2 | 996 | 67.67% | 1.34% (9/674) | 66.77% | 0 | 0.003489 | 3.85% |
| Zipf exponent 0.6 | 4944 | 88.45% | 0.80% (35/4373) | 87.74% | 0 | 0.023087 | 3.82% |
| Zipf exponent 0 (uniform) | 4925 | 88.35% | 0.80% (35/4351) | 87.63% | 0 | 0.022944 | 3.81% |
| 200 output tokens | 4965 | 89.24% | 0.68% (30/4431) | 88.64% | 0 | 0.356220 | 0.27% |

## Reading

- **The hit-rate is the repetition rate.** Every correct hit at τ = 0.95 is
  an exact repeat; the lexical embedder contributes no correct hit on a text
  it has not seen, and every hit on such a text is a trap. Across seeds and
  Zipf exponents the hit-rate stays within one point of the exact-match
  hit-rate, and it falls to 67% when the same 620 texts are spread over 1000
  requests instead of 5000.
- **τ trades traps for paraphrases badly.** Down to τ = 0.80 the embedder
  finds 30 correct paraphrase hits at the price of 175 false hits; no τ in
  the sweep gains a paraphrase hit without false hits. This is the lexical
  embedder failing as `make calibrate` reports it fails on the seed pairs; it
  says nothing about a semantic embedder.
- **Savings clear the toll by a wide margin here.** The toll is 3.8% of the
  eligible provider spend with the mock's 11-token answers and 0.27% with
  200-token answers, against a correct-hit rate of 88.64%; the false hits'
  skipped spend (0.000170) is not in the savings. With prices from flags and
  answers of fixed length, the dollar figures show the arithmetic, not a
  provider bill.
- **Latency follows the hit-rate.** The median request becomes a hit and
  takes the in-process lookup (0.7 ms) instead of the provider's draw
  (212 ms); p95 is set by misses, which pay lookup plus provider. The lookup
  is the local embedder plus a flat scan of up to 5000 entries in one
  process; an HTTP embedder adds its network round trip to every eligible
  request, hit or miss, and that is not measured here. Measured lookup times
  vary between runs on the same machine (embed + search p95 from 1.5 ms to
  3.9 ms over repeated default runs); every count and dollar figure is
  identical across runs with the same flags.

## Limits

- **Synthetic corpus.** One author, one domain, short questions, unique
  texts, uniform paraphrase choice; the repetition rate is a consequence of
  the request count over 620 texts.
- **Lexical embedder.** `local` hashes character trigrams; its similarity
  measures shared spelling, not meaning. The numbers measure the replay
  machinery and this embedder, not paraphrase recognition.
- **Shadow-only gateway.** The gateway serves nothing from the cache. The
  replay derives what a serving cache would have answered from shadow
  evaluations; as in the shadow, every eligible request is inserted,
  including would-be hits, and the namespace uses the route's model because
  no provider answers.
- **Mock latencies and synthetic prices.** Provider latency comes from the
  mock's two-point model; prices and answer length come from flags. The
  index's infrastructure cost is not modeled because the index is
  in-process. One process replays requests one after another, without
  concurrency.
- **No bound on precision.** Hits on repeated pairs are not independent, so
  the false-positive rate is an observed rate for this traffic, not a
  certified precision; certifying τ belongs to the calibration harness.

## Reproduce

```
make replay                                  # defaults above
make replay REPLAY_FLAGS="-seed 2"           # another traffic sample
make replay REPLAY_FLAGS="-requests 1000 -output-tokens 200"
```

Outputs go to the ignored `out/replay/`: `sweep.csv` (one row per τ,
`hit_rate` next to `fp_rate`, the configured τ marked `chosen`),
`requests.csv` (one row per request with its neighbor, similarity, labels,
lookup and provider latency and costs) and `summary.md`. Undefined ratios are
written `undefined`.
