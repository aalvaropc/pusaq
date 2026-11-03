## 1. Shared entry points

- [ ] 1.1 Expose the mock provider's latency model as `Options.SampleLatency` and draw the server's latencies from it; the existing sampler test passes unchanged.
- [ ] 1.2 Add `semcache.Shadow.Evaluate`, returning the neighbor, embed and search latency and whether the entry was inserted, and `Neighbor.Hit`; the workers call `Evaluate` and the records use `Hit`; test both, including the τ boundary.

## 2. Corpus and replay

- [ ] 2.1 Write the labeled EN/ES corpus under `testdata/replay/`; test that it loads, that every intent has at least four paraphrases in both languages, that every trap family has look-alike intents, and that the time-dependent intent is ineligible.
- [ ] 2.2 Implement the Zipf sampler and the traffic model; test determinism, exact probabilities and the sampled shape.
- [ ] 2.3 Implement the replay and the evaluation of a τ; test hit and false-hit accounting on a hand-built corpus with known neighbors, ineligible requests, cross-language neighbors, savings, wrong-hit spend, break-even and nearest-rank percentiles.
- [ ] 2.4 Mutation check of the accounting (false hits counted as correct, toll omitted, toll charged on ineligible requests, wrong hits counted as savings, insertion before lookup, hit-rate over all requests, inverted Zipf exponent).

## 3. Command, run and report

- [ ] 3.1 Add `cmd/cachereplay` with validated flags and the CSV and Markdown outputs, and `make replay`; test flag validation and the outputs.
- [ ] 3.2 Run `make replay` with defaults and record the numbers, parameters, machine and limits in `docs/measurements/cache-replay.md`; link it from the measurement index, `docs/gateway.md` and `docs/design.md`.
- [ ] 3.3 `make test`, `make lint` and `openspec validate --all` pass.
