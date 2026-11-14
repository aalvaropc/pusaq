## Why

The shadow cache records what it would have hit, and `cachecal` certifies τ on labeled pairs, but nothing answers the operational question: what would the semantic cache have done on a stream of traffic? How often would it hit, how many of those hits would serve a wrong answer, does the provider spend it avoids cover the embedding toll, and what does it do to latency? Constitution rule 3 publishes the false-positive rate next to every hit-rate, and a false positive is only countable against labels that real traffic does not carry. A replay of labeled, Zipf-distributed traffic through the shadow's own evaluation path measures all of it locally, at no cost.

## What Changes

- A labeled replay corpus under `testdata/replay/`: intents with five paraphrases each in English and Spanish, grouped in families of look-alike intents that need different answers (entity, action, negation, date and quantity changes), plus one time-dependent intent the content rules keep out of the cache.
- New package `internal/replay`: a seeded Zipf traffic model over the corpus' answers with uniform paraphrase choice and provider latencies drawn from the mock provider's latency model; a replay that drives each request through the gateway's shadow evaluation path (namespace derivation, content-rule eligibility, embedder, nearest neighbor, insertion after evaluation) and keeps, per request, the best similarity, whether the neighbor's labeled answer matches, the measured embed and search time and the request's provider and embedding cost; and the evaluation of any τ from that record: hit-rate with its false-positive rate and counts, net savings, the spend skipped by wrong hits reported apart, latency p50/p95 with and without the cache, and the break-even hit-rate.
- `semcache.Shadow.Evaluate` runs one shadow evaluation synchronously and returns its outcome (neighbor, embed and search latency, insertion); the asynchronous workers call it. `semcache.Neighbor.Hit` is the would-be-hit rule the shadow records and the replay share.
- `mockprovider.Options.SampleLatency` exposes the mock provider's latency model; the mock server draws from it.
- New command `cmd/cachereplay` and target `make replay` write the τ sweep CSV, a per-request CSV and a Markdown summary under the ignored `out/replay/`.
- `docs/measurements/cache-replay.md` records a run with its parameters, results and limits; the measurement index, `docs/gateway.md` and `docs/design.md` link it.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `semantic-cache`: adds the requirement for a labeled-traffic replay that reports the hit-rate with its false-positive rate, net savings after the embedding toll, latency with and without the cache, and the break-even hit-rate.

## Impact

New `internal/replay`, `cmd/cachereplay` and `testdata/replay/`, a Makefile target and documentation. `internal/semcache` and `internal/mockprovider` gain exported entry points with unchanged behavior. The gateway's request path, its events and its configuration are unchanged; no dependency is added.
