# Tasks: add-content-rules

## 1. Content rules

- [x] 1.1 `internal/policy`: sensitive, injection and temporal rules (kinds only), Luhn and mod-97 checks, temporal masks; unit tests per family with the traps as negatives.
- [x] 1.2 `testdata/policy/content-rules.csv` (176 rows EN/ES) and `TestContentRulesCorpus` enforcing the published floors.

## 2. Gateway

- [x] 2.1 `temperature` parsed, forwarded through one `upstreamRequest` helper, keyed in the namespace; `semcache.PolicyVersion = "v1"`.
- [x] 2.2 `applyPolicy` classifies by role, sets `policy_flags`, blocks per tenant (`sensitive`, `injection`); `cacheEligibility` with one recorded reason; `offerShadow` gated by it; `cache_skip` and `policy_flags` on both event paths and in the log sink.
- [x] 2.3 Config keys `sensitive`, `injection`, `cache`; mapping in `cmd/pusaq/main.go`; tests.

## 3. Docs and delivery

- [x] 3.1 `docs/measurements/content-rules.md` with the corpus numbers; README (status, API, policies, cache, config, measurements); `docs/design.md` §4 step 3, §5.4, §8.
- [x] 3.2 Suite `-race`, lint, `make up && make smoke` with a flagged prompt; conventional commits; draft PR.
