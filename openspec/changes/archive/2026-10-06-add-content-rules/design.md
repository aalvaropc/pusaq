# Design: add-content-rules

## Context

The policy filter (`internal/gateway/policy.go`) checks models, prompt size and budget; the shadow cache (`offerShadow`) checked only the request's shape (one user message). `temperature` was neither parsed nor forwarded, and the namespace carried a `temp=none` placeholder. Cost events carried nothing about content. See proposal.md.

## Goals / Non-Goals

**Goals:**

- Prompts with sensitive data, instruction overrides, time dependence or high temperature never enter the cache; the reason is on the event.
- Tenants can turn sensitive-data and injection matches into denials.
- Every rule's precision and recall is a published number on a labeled corpus, with floors the suite enforces.

**Non-Goals:**

- Rewriting or masking content (the gateway never logs prompt text, and the cache never sees flagged prompts); a learned classifier; outbound secret scanning of responses; a managed DLP backend; tools or response-format awareness (not in the API subset).

## Decisions

1. **A pure package with three families and kinds-only output.** `policy.Sensitive`, `policy.Injection`, `policy.Temporal` return sorted, deduplicated kind names; `Classify` composes them for the corpus test. Matched text never leaves the detector, so flags can go on events and into 403 messages.

2. **Role-split scanning in the gateway.** Sensitive data is scanned in every role; injection phrasing outside the system prompt (applications legitimately write "you are now a pirate" there); temporal cues only in user messages (system prompts routinely state today's date). Scanning system prompts for the latter two would mark every request of such an application.

3. **Content denial before the budget read.** Order in `applyPolicy`: models → prompt cap → classify → block (if the tenant says so) → budget. The flags are set on the request before the decision, so a 403's event explains itself; a content denial never reaches the store or the provider.

4. **One eligibility decision, one reason.** `cacheEligibility` (`internal/gateway/cache.go`) returns the first reason in a fixed order — `tenant_off`, `multi_turn`, `sensitive`, `injection`, `temporal`, `temperature` — and the shape the cache needs; `offerShadow` no longer inspects the request. The reason is `cache_skip` on the event.

5. **Temperature: forwarded, keyed, thresholded.** A `*float64` end to end keeps an explicit 0 and tells an omitted value apart; one `upstreamRequest` helper feeds both the buffered and the streaming path. The namespace takes `strconv.FormatFloat(t, 'g', -1, 64)` or `none`. An explicit value above 0.7 is ineligible; an omitted one is eligible under its own `none` key — treating it as the provider default would remove almost all real traffic from shadow measurement, which is what the cache exists to do today. `semcache.PolicyVersion = "v1"` separates entries inserted under the old rules.

6. **RE2 with masks, checksums, and cue words.** `\b` is ASCII in RE2, so Spanish alternations use explicit Unicode boundaries. Temporal idioms that contain a cue without meaning "now" ("today's world", "currently selected", "por la mañana", "hoy en día") are masked before the cues run — there is no lookaround. Card numbers need a known issuer prefix and a Luhn checksum; IBANs need mod-97; bare digit runs count as phones only next to a phone cue. Injection needs verb + determiner + "previous/anterior" + instruction noun adjacent, or a role cue plus a no-rules cue within 40 characters, so "the previous instructions were unclear" and "act as a tour guide" pass.

7. **Floors that the suite enforces.** Sensitive precision ≥ 0.95 / recall ≥ 0.85; injection ≥ 0.90 / ≥ 0.70; temporal ≥ 0.90 / ≥ 0.80 — on ~30 positives per flag one miss is ~3 points, so finer floors would be noise. The corpus holds hard negatives (order numbers, ISBNs, IPs, versions, Luhn-failing digit runs, benign "instructions", "current directory") and implicit-temporal positives the rules cannot see, so recall says so.

## Risks / Trade-offs

- The corpus is author-labeled, not blind; perfect scores on two families reflect that the patterns and the corpus share an author. The doc says so; the floors are what the suite guards.
- Paraphrased injection without the listed lexemes passes; a developer asking to "print the system prompt" is flagged. Stated in the doc; blocking is opt-in per tenant.
- Every request pays a linear scan over its messages, bounded by the body cap (1 MiB); the shadow embedding already costs far more.
