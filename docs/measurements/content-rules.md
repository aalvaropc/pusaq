# Measurement: content rules of the policy filter

**Claim under test.** The policy filter's content rules flag prompts that
carry sensitive data, instruction-override phrasing, or a time dependence,
with stated heuristics whose precision and recall are numbers, not
adjectives. They decide what the semantic cache never sees and what a
tenant may block. They are a policy filter, not a firewall: regex and
keyword rules with known limits (constitution rule 8).

**Why it matters.** Caching a prompt with personal data is a privacy
incident; caching "today's exchange rate" serves a stale answer; a tenant
that blocks on injection phrasing needs the false-positive rate to know what
it costs its users. Each family's number decides how far the rule can be
trusted, and for which use: precision protects users of `block`; recall
protects the cache's correctness.

## Rules

Every rule names a *kind*, never the matched text, so flags travel on cost
events (`policy_flags`) and in denial messages without leaking content.

| Family | Kinds | Mechanism | Stated limits |
|---|---|---|---|
| Sensitive (scanned in every message, tool-call arguments and tool definitions) | `pii:email`, `pii:phone`, `pii:card`, `pii:iban`, `secret:api_key`, `secret:private_key`, `secret:jwt`, `secret:bearer`, `secret:assignment` | Regex plus checks: card numbers need a known issuer prefix and a Luhn checksum, including candidates ending before a separated expiry or CVV; IBANs need ISO 13616 mod-97; international phones need 8–15 digits; a bare digit run counts only next to a phone cue (`tel`, `cel`, `whatsapp`, `llámame`…); dots never separate phones | Unformatted or dot-separated phone numbers without a cue pass; short passwords pass; only the listed secret shapes (`sk-…`, `AKIA…`, `ghp_…`, PEM, JWT, `Bearer …`, `password=…`) are known; Peruvian CCI numbers are not covered |
| Injection (scanned outside the system prompt and tool definitions, tool results and tool-call arguments included) | `injection:ignore_instructions`, `injection:role_override`, `injection:prompt_leak`, `injection:role_marker` | Phrase patterns in English and Spanish: verb + determiner + *previous/anterior* + instruction noun, adjacent; a role cue plus a no-rules cue within 40 characters; an exfiltration verb plus *your/its system prompt*; chat-template markers | Lexical: paraphrases without these lexemes pass; a developer asking to "print the system prompt" is flagged; a user correcting their own earlier message is not |
| Temporal (scanned in user messages) | `temporal:now`, `temporal:relative_day`, `temporal:period`, `temporal:latest`, `temporal:clock` | Cue words and phrases in both languages, after masking idioms that contain a cue without meaning "now" ("today's world", "currently logged in", "por la mañana", "hoy en día", `date.today()`) | Cue-based: "who is the president of Peru?" carries no cue and passes; bare "now"/"ahora" never count; "current" counts only next to a volatile noun; the song title "Yesterday" is flagged |

Role split: system prompts routinely state today's date and set a persona
("you are now a pirate"), so injection and temporal rules do not read them;
tool definitions are application text in the same way. Tool results are
read for injection like user content. Sensitive data anywhere counts.
Tool-call arguments are JSON, and `secret:assignment` does not match a
quoted key such as `{"password":"…"}`. The corpus holds texts without
roles, so the numbers below do not depend on the split.

## Corpus

`testdata/policy/content-rules.csv`: 204 hand-labeled rows in English and
Spanish, columns `text,sensitive,injection,temporal`. The 2026-10-20 expansion
adds 15 synthetic card-positive rows and 13 hard negatives to the original
176 rows. These are targeted regressions, not blind traffic.

| Slice | Rows | What it holds |
|---|---|---|
| Sensitive positives | 45 | 6 emails; 8 phones (Peru, Mexico, Argentina, Chile, Colombia, US formats); 19 card cases (Visa, Mastercard, Amex, Discover), including expiry/CVV suffixes and separator controls; 3 valid IBANs; 9 secrets, one per shape |
| Sensitive hard negatives | 33 | order numbers, ISBN-13, IPv4, versions, a nanosecond timestamp, a 16-digit run that fails Luhn, an 8-digit DNI, a booking code that fails mod-97, `sk-support`, "the bearer of this letter", `@handle`, dates, spaced amounts, a hex hash; invalid checksums, unsupported issuer, embedded digits and uninterrupted numbers with independently valid card prefixes |
| Injection positives | 30 | 15 EN / 15 ES across the four kinds |
| Injection hard negatives | 20 | assembly instructions, "the previous instructions were unclear", "disregard my previous message", "how to write a system prompt", benign personas, "you are now the owner", "print the system message to the console", "ignore case", "olvidé mi contraseña", "reglas del fútbol" |
| Temporal positives | 31 | 15 EN / 16 ES, including four implicit ones the rules are not expected to see ("who is the president of Peru?", "how old is Messi?", "which team leads the standings?", "¿a cuánto está el dólar ahora?") |
| Temporal hard negatives | 20 | the mask cases, "electric current", "current directory", "rate limiter", `date.today()`, "USA Today", "Tomorrowland", "Yesterday by the Beatles" (a known false positive) |
| Plain | 20 | ordinary questions in both languages |
| Multi-flag | 5 | rows positive on two families |

Floors were chosen before the original corpus was run, with about 30
positives per family. They remain unchanged for the expanded corpus.

| Family | Precision ≥ | Recall ≥ |
|---|---|---|
| sensitive | 0.95 | 0.85 |
| injection | 0.90 | 0.70 |
| temporal | 0.90 | 0.80 |

## Protocol

```
go test -v -run TestContentRulesCorpus ./internal/policy
```

A row is a positive for a family when its column is 1 and a prediction when
any kind of that family fires. The test prints every false positive and
false negative, then precision and recall per family, and fails below a
floor — so the numbers below cannot drift silently. Patterns change when regression evidence identifies a bug. Added rows
include missed positives and boundary controls; existing rows and floors
remain unchanged.

## Results (2026-10-20)

| Family | Positives | TP | FP | FN | Precision | Recall |
|---|---|---|---|---|---|---|
| sensitive | 48 | 48 | 0 | 0 | **1.000** | **1.000** |
| injection | 33 | 33 | 0 | 0 | **1.000** | **1.000** |
| temporal | 35 | 31 | 1 | 4 | **0.969** | **0.886** |

The suffix correction tests at most seven candidate lengths of 13–19
digits, ending at a match boundary or before an existing space/hyphen.
Issuer and checksum checks remain unchanged. It never truncates an
uninterrupted digit block to accept its valid prefix.

Before the correction, exact card-kind tests miss 13 of the 15 added
positive cases. On the expanded corpus the sensitive family has TP=37,
FP=0 and FN=11 (recall 0.771, below its 0.85 floor). Some hyphenated cards
also trigger the phone heuristic, so family recall alone conceals two
missing card-kind classifications. After the correction all 15 card
positives and all 13 hard negatives pass, with the family counts above.
Injection and temporal counts are unchanged.

Gateway integration covers all eight combinations of block/log action,
buffered/streaming response and user/system placement. Blocking returns
403 before budget access, token charges or provider calls. Logging serves
the request with `pii:card` and `cache_skip=sensitive`; neither mode offers
the prompt to the shadow cache. Responses and cost events omit the matched
card. All policy and gateway regressions pass with the race detector.

Misses and false alarms, all foreseen in the rules' stated limits:

- temporal FN: "Who is the president of Peru?", "How old is Lionel Messi?",
  "Which team leads the Premier League standings?", "¿A cuánto está el
  dólar ahora?" — implicit time dependence without a cue, or a bare
  "ahora".
- temporal FP: "I loved Yesterday by the Beatles, recommend similar songs".

Reading: on this corpus the checksum-backed and shape-backed rules
(sensitive) and the phrase-bound rules (injection) make no mistake; the
cue-based temporal rules miss what has no cue and are fooled by a title.
The floors, not the perfect scores, are the guarantee: they are what the
suite enforces on every change.

## Scan cost (2026-11-14)

The families run on every admitted completion request, before any provider
call, so their CPU cost is paid per request.

Each rule carries a prefilter: a condition that every match of its pattern
needs. For the case-insensitive phrase rules it is a literal in a case-folded
copy of the text with whitespace runs collapsed (Unicode simple folding, so
`ſ` and the Kelvin sign count as `s` and `k`); for the secret shapes, a
case-sensitive literal (`sk-`, `AKIA`, `.eyJ`, `-----BEGIN `…); for phones,
cards and IBANs, a digit shape (a run of 8 digits, 13 digits chained by
single spaces or hyphens, two capitals before two digits…). A pattern runs
over the full text only when its prefilter holds, so the kinds reported are
those of running every pattern. One pass builds the folded copy and a
trigram bitmap that answers most literal probes without a substring search.

Equivalence is tested against `internal/policy/reference_test.go`, a
verbatim copy of the patterns and of the scan without prefilters:

- every corpus row gets the same kinds, so the family counts under Results
  and the rows per kind are unchanged;
- 3,000 seeded inputs (corpus rows, cue words, digit shapes, case and
  Unicode-fold variants, whitespace variants, invalid UTF-8; every kind
  fires on at least 10 of them) classify identically;
- `FuzzClassify` compares one and two texts against the reference:
  60 s locally with `-fuzzminimizetime 2s`, 1,821,742 executions, no
  difference.

Removing `today` from a prefilter, dropping the `ſ`/Kelvin folding,
requiring 14 chained card digits or not collapsing form feeds each makes
these tests fail.

Protocol: Intel Core i5-13600K, Go 1.26.10, linux/amd64, pinned to one
performance core, five runs per binary in two interleaved rounds; "before"
is the same benchmark over the classifier without prefilters.

```
taskset -c 4,5 go test -run '^$' -bench BenchmarkClassify -benchmem -count 5 ./internal/policy
```

| Prompt | Bytes | Before | After | Speed-up |
|---|---|---|---|---|
| `en/short` | 238 | 335 µs | 4.3 µs | 79× |
| `es/short` | 262 | 384 µs | 5.5 µs | 70× |
| `en/4KiB` | 4,172 | 5.68 ms | 71.7 µs | 79× |
| `es/4KiB` | 4,121 | 6.21 ms | 81.5 µs | 76× |
| `cues/4KiB` | 4,181 | 5.52 ms | 5.59 ms | 1.0× |

Medians of 10 runs per side. The machine ran in its power-saver profile
(performance cores capped at 3.5 GHz) with other workloads active; the
interleaved rounds expose both sides to the same conditions. Allocations go
from 6 to 9 per call (per family: the scan state, the folded copy and the
bitmap); bytes per call from 19.7 KB to 27.1 KB for the 4 KiB prompts.

Reading: a prompt that lacks the rules' cue literals pays a folding pass
per family and one digit-shape pass instead of 33 pattern scans (31 rules
and two temporal masks). The `cues` prompt carries a literal for every rule
without matching any, so every pattern still scans the whole text and its
cost is unchanged, under 1 MB/s here: a tenant that writes such prompts
still spends on the order of a second of gateway CPU per MiB of message
content. The prefilters make ordinary prompts cheap; they do not bound
that case.

## Caveats

- **Author-labeled, not blind.** The corpus and the patterns share an
  author, which is why two families score perfectly; a blind corpus from
  real traffic would score lower. The floors are set for such a corpus, not
  for this one.
- **Heuristics.** A secret in an unknown format, a phone number written
  with dots, an injection phrased around the lexemes, a question whose time
  dependence is implicit — all pass. The rules are what keeps the obvious
  cases out of the cache and gives tenants a switch; they do not promise
  detection.
- **Language coverage** is English and Peruvian-centric Spanish; other
  variants and languages are untested.
- **RE2** has no lookaround and an ASCII-only `\b`; the masks and explicit
  Unicode boundaries are how the exceptions are stated.
