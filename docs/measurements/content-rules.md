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
| Sensitive (scanned in every message) | `pii:email`, `pii:phone`, `pii:card`, `pii:iban`, `secret:api_key`, `secret:private_key`, `secret:jwt`, `secret:bearer`, `secret:assignment` | Regex plus checks: card numbers need a known issuer prefix and a Luhn checksum; IBANs need ISO 13616 mod-97; international phones need 8–15 digits; a bare digit run counts only next to a phone cue (`tel`, `cel`, `whatsapp`, `llámame`…); dots never separate phones | Unformatted or dot-separated phone numbers without a cue pass; short passwords pass; only the listed secret shapes (`sk-…`, `AKIA…`, `ghp_…`, PEM, JWT, `Bearer …`, `password=…`) are known; Peruvian CCI numbers are not covered |
| Injection (scanned outside the system prompt) | `injection:ignore_instructions`, `injection:role_override`, `injection:prompt_leak`, `injection:role_marker` | Phrase patterns in English and Spanish: verb + determiner + *previous/anterior* + instruction noun, adjacent; a role cue plus a no-rules cue within 40 characters; an exfiltration verb plus *your/its system prompt*; chat-template markers | Lexical: paraphrases without these lexemes pass; a developer asking to "print the system prompt" is flagged; a user correcting their own earlier message is not |
| Temporal (scanned in user messages) | `temporal:now`, `temporal:relative_day`, `temporal:period`, `temporal:latest`, `temporal:clock` | Cue words and phrases in both languages, after masking idioms that contain a cue without meaning "now" ("today's world", "currently logged in", "por la mañana", "hoy en día", `date.today()`) | Cue-based: "who is the president of Peru?" carries no cue and passes; bare "now"/"ahora" never count; "current" counts only next to a volatile noun; the song title "Yesterday" is flagged |

Role split: system prompts routinely state today's date and set a persona
("you are now a pirate"), so injection and temporal rules do not read them;
sensitive data anywhere counts.

## Corpus

`testdata/policy/content-rules.csv`: 176 hand-labeled rows, roughly 55 %
English and 45 % Spanish, columns `text,sensitive,injection,temporal`.

| Slice | Rows | What it holds |
|---|---|---|
| Sensitive positives | 30 | 6 emails; 8 phones (Peru, Mexico, Argentina, Chile, Colombia, US formats); 4 Luhn-valid cards (Visa, Mastercard, Amex, Discover); 3 valid IBANs; 9 secrets, one per shape |
| Sensitive hard negatives | 20 | order numbers, ISBN-13, IPv4, versions, a nanosecond timestamp, a 16-digit run that fails Luhn, an 8-digit DNI, a booking code that fails mod-97, `sk-support`, "the bearer of this letter", `@handle`, dates, spaced amounts, a hex hash |
| Injection positives | 30 | 15 EN / 15 ES across the four kinds |
| Injection hard negatives | 20 | assembly instructions, "the previous instructions were unclear", "disregard my previous message", "how to write a system prompt", benign personas, "you are now the owner", "print the system message to the console", "ignore case", "olvidé mi contraseña", "reglas del fútbol" |
| Temporal positives | 31 | 15 EN / 16 ES, including four implicit ones the rules are not expected to see ("who is the president of Peru?", "how old is Messi?", "which team leads the standings?", "¿a cuánto está el dólar ahora?") |
| Temporal hard negatives | 20 | the mask cases, "electric current", "current directory", "rate limiter", `date.today()`, "USA Today", "Tomorrowland", "Yesterday by the Beatles" (a known false positive) |
| Plain | 20 | ordinary questions in both languages |
| Multi-flag | 5 | rows positive on two families |

Floors, chosen before the corpus was run: with ~30 positives per family one
miss is about three points, so coarser floors are the only honest ones.

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
floor — so the numbers below cannot drift silently. Patterns were adjusted
against the corpus only where a row revealed a genuine pattern bug, never by
adding rows that pass.

## Results (2026-09-30)

| Family | Positives | TP | FP | FN | Precision | Recall |
|---|---|---|---|---|---|---|
| sensitive | 33 | 33 | 0 | 0 | **1.000** | **1.000** |
| injection | 33 | 33 | 0 | 0 | **1.000** | **1.000** |
| temporal | 35 | 31 | 1 | 4 | **0.969** | **0.886** |

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

## Caveats

- **Author-labeled, not blind.** The corpus and the patterns share an
  author, which is why two families score perfectly; a blind corpus from
  real traffic would score lower, and its numbers would replace these. The
  floors were set for that corpus, not this one.
- **Heuristics.** A secret in an unknown format, a phone number written
  with dots, an injection phrased around the lexemes, a question whose time
  dependence is implicit — all pass. The rules are what keeps the obvious
  cases out of the cache and gives tenants a switch; they do not promise
  detection.
- **Language coverage** is English and Peruvian-centric Spanish; other
  variants and languages are untested.
- **RE2** has no lookaround and an ASCII-only `\b`; the masks and explicit
  Unicode boundaries are how the exceptions are stated, and they are the
  first place to look when a new false positive appears.
