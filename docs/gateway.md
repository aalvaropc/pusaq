# Gateway reference

Request and response contracts for the implemented gateway. See
[configuration](configuration.md) for environment variables,
[operations](operations.md) for setup, and
[measurements](measurements/README.md) for dated evidence.

## API

`POST /v1/chat/completions` accepts this OpenAI-compatible subset:

| Field | Contract |
|---|---|
| `model` | Required alias: `pusaq/fast`, `pusaq/best` or `pusaq/auto`. Physical models are configuration. |
| `messages` | Required nonempty array; message content is a string. |
| `max_tokens` | Optional positive integer; also determines the output reservation. Each value, and omission, has its own cache namespace. |
| `temperature` | Optional number forwarded as sent, including explicit zero; omission has its own cache namespace. |
| `stream` | Optional boolean; `true` selects Server-Sent Events. |
| `metadata.feature_tag` | Optional routing label, at most 64 letters, digits, `.`, `_` or `-`. |
| `metadata.run_id` | Optional string, at most 64 bytes, recorded before RPM admission for measurement filtering. |

A buffered response carries `id`, `object`, `created`, `model`,
`system_fingerprint`, `choices` and `usage` (with `total_tokens`). `created`
is the provider's Unix timestamp, or the gateway's when the provider sends
none; `system_fingerprint` is present only when the provider sends one.

The default `pusaq/auto` rules map `classify`/`extract` to `pusaq/fast`,
`reason`/`code` to `pusaq/best`, and absent or unknown tags to `pusaq/fast`.
`PUSAQ_ROUTING_RULES` replaces that table.

Tool and function calling, `response_format`, multiple choices, logprobs,
audio, non-text modalities, prediction, web search and
`max_completion_tokens` are rejected with 400 `invalid_request` naming the
unsupported field. Message tool/function/audio fields and arrays of content
parts are also rejected. For unsupported fields, `null` or `false` is
treated as absent; `n: 1` and `modalities: ["text"]` are harmless accepted
values. Other unrecognized fields are ignored rather than forwarded.

### Response headers

| Header | Meaning |
|---|---|
| `X-Pusaq-Model` | Route's physical model; also present on errors after resolution. |
| `X-Pusaq-Route-Reason` | `direct`, `rule:<tag>`, `default:untagged` or `default:unknown_tag`, once resolved. |
| `X-Pusaq-Request-Id` | Correlation ID on every completion response and every error. Only requests that enter the accounting pipeline have a cost event with it. |
| `X-Pusaq-Region` | Replica label when `PUSAQ_REGION` is configured. |

The response body's `model`, and the `model` of each forwarded stream chunk,
is the name the provider reported. It can differ from `X-Pusaq-Model`, for
example a dated version behind an upstream alias.

### Errors

All gateway errors, including unknown paths and unsupported methods, use
`{"error":{"code":"...","message":"...","type":"...","param":...}}` and
carry `X-Pusaq-Request-Id`. `code` is the precise, stable reason; `type` is
an OpenAI-style category derived from it. `param` names the offending
request field of a validation error (`model`, `messages`, `max_tokens`,
`messages[].content`, `metadata.feature_tag`, an unsupported field, …) and
is `null` otherwise. Retryable quota and readiness errors also carry
`retry_after_ms` and a `Retry-After` header; 405 answers carry `Allow`.
Provider bodies and diagnostic strings are not exposed.

| Code | HTTP status | `type` | Meaning |
|---|---|---|---|
| `invalid_request` | 400 | `invalid_request_error` | Invalid payload, unsupported field, oversized body or provider rejection (400/413/422). |
| `unauthorized` | 401 | `authentication_error` | Missing or unknown tenant key. |
| `budget_exceeded` | 402 | `insufficient_quota` | Monthly budget exhausted; retry guidance reaches the next UTC month. |
| `policy_denied` | 403 | `permission_error` | Tenant model, prompt-size or content policy rejected the request. |
| `not_found` | 404 | `not_found_error` | Path the gateway does not serve. |
| `method_not_allowed` | 405 | `invalid_request_error` | Served path requested with another method. |
| `rate_limited` | 429 | `rate_limit_error` | Request or token bucket cannot admit the request. |
| `client_canceled` | 499 | `invalid_request_error` | Client left before a response was established. |
| `internal` | 500 | `server_error` | Internal failure normalized by the gateway. |
| `upstream_error` | 502 | `server_error` | Provider transport, timeout, response or stream failure. |
| `store_unavailable` | 503 | `server_error` | Store unavailable for a tenant configured to fail closed. |
| `not_ready` | 503 | `server_error` | An injected readiness window refuses an authenticated request before its body is read. |

### Streaming

With `stream: true`, responses carry `text/event-stream`. The provider is
asked for a usage chunk with `stream_options.include_usage`; a successful
stream ends with `data: [DONE]`.

Each complete SSE event is validated before forwarding. Event framing is
capped at 1 MiB. A provider error event, premature EOF or oversized event
terminates the stream with one generic `upstream_error` event and no
`[DONE]`. An established stream is never retried on another backend.
Content fingerprints are hashed incrementally without retaining the full
response. A client disconnect or failed write/flush closes the upstream and
finalizes accounting; interrupted streams record `stream_interrupted`.

## Authentication and I/O limits

Configure tenants with `PUSAQ_API_KEYS="key:tenant,..."`; clients send
`Authorization: Bearer <key>`. The scheme is case-insensitive, and spaces or
tabs may separate it from the key. Missing or unknown keys return 401 before
the body is read, and their accounting events have no payload-derived run ID.

Authenticated bodies are limited to 1 MiB and read within
`PUSAQ_BODY_READ_TIMEOUT` (default 10 s). Body failures and malformed run IDs
produce one accounted `invalid_request` error before RPM admission. Valid
run IDs are retained even when RPM rejects the request. The body deadline
is cleared before the response stream begins.

`PUSAQ_WRITE_IDLE_TIMEOUT` defaults to 30 s per response write/flush and is
refreshed during SSE. It is not a total stream lifetime limit. The HTTP
server also uses a 10 s header timeout and a 120 s idle-connection timeout.

With `PUSAQ_DEV_MODE=true` and `PUSAQ_API_KEYS` unset, requests belong to
the unauthenticated `dev` tenant and startup logs a warning. Missing keys
outside development mode, an empty key setting, or combining development
mode with keys prevents startup. Configuration errors identify positions
or tenants without printing keys. `/healthz` and `/readyz` are public.

## Rate limiting

With `PUSAQ_REDIS_ADDR` set, each tenant gets a token
bucket in Redis decided by one atomic Lua script shared by every replica —
read-decide-decrement as a single operation, so the limit stays exact under
concurrency (proven by test: exactly the bucket capacity admitted out of 200
concurrent requests across two instances). Denials are 429 `rate_limited`
with `retry_after_ms` and a `Retry-After` header. An idle bucket is
reclaimed only once its refill would have filled it again, and no sooner
than one minute after its last update, so reclaiming never admits more than
the refill allows. If Redis is unreachable
the gateway **fails open** within a 250 ms budget per store call —
temporarily inexact limits beat an outage caused by the limiter itself —
unless the tenant's policy sets `fail_closed`, in which case it answers 503
`store_unavailable`. A request let through this way carries `fail_open` on
its cost event, naming each decision the store could not make: `rpm`,
`tpm`, or `budget`.
Unset `PUSAQ_REDIS_ADDR` disables limiting with a startup warning.

Two modes, chosen with `PUSAQ_RATELIMIT_MODE`. **`strict`** (default): one
atomic store decision per request, exact across replicas. **`lease`**: each
replica takes up to `PUSAQ_LEASE_SIZE` request tokens per round-trip and
admits locally; unused tokens return when the lease passes `PUSAQ_LEASE_TTL`
or the replica drains, each return within its own 250 ms store budget, and a
store denial is cached locally until its retry
time or the lease TTL, whichever is sooner. Cleanup revalidates entry ownership
and never waits for a tenant while holding the shared tenant-map mutex.
With successful store operations, admissions follow the bucket in both modes; in lease
mode up to `replicas × lease size` tokens can sit unspent in leases at any
moment, and a replica that dies without draining strands at most one lease
per tenant (under-admission, never overrun). Lease mode covers request
limits only; token limits are always strict. Both modes are measured with
the same load test.

### Token limits (TPM)

Alongside RPM, each tenant has an LLM-token bucket.
After model resolution and tenant policy checks, a request is charged its
input estimate (message role and content bytes divided by four, rounded up)
plus an output reservation (`max_tokens`, or `PUSAQ_TPM_RESERVE_DEFAULT`);
when actual usage is known the difference is reconciled back atomically —
on success, on error, and at stream end. A confirmed reservation is fully
returned if no provider was invoked. After any provider invocation, unknown
usage keeps the charge, including errors, timeouts and abandoned streams.
Every charged request's cost event records `tokens_charged` next to actual
usage, so the estimate's error is measurable from the event stream; a charge
skipped because the store failed open leaves `tokens_charged` out and records
`fail_open` `tpm`, so it is not mistaken for token limiting turned off. In the
[mock experiment](measurements/tpm-overrun.md), window consumption stays
within the stated tolerance and peak excess stays below the experiment's
reservation-error bound. Those numerical bounds depend on the recorded
usage and concurrency; the gateway does not know actual output length at
admission.
`tokens_charged` is the initial reservation, not net consumption after settlement.

Refill uses Redis time inside each atomic Lua operation, with a timestamp
that never moves backward. A stored timestamp ahead of Redis time pauses
refill until catch-up; retry times include that delay. Buckets retain their
keys and balances across the change. This requires Redis 7 or newer; gateways
sharing a bucket use the same implementation.

## Resilience

Each upstream backend is guarded by a circuit breaker
(closed / open / half-open). Defaults: opens on a >50% failure rate over a
20-sample window with a 10-sample minimum, 10s cooldown, 3 half-open
probes — configurable with `PUSAQ_BREAKER_WINDOW`, `PUSAQ_BREAKER_MIN_SAMPLES`,
`PUSAQ_BREAKER_THRESHOLD`, `PUSAQ_BREAKER_COOLDOWN`, `PUSAQ_BREAKER_PROBES`,
validated and logged at startup. Every transition is one `breaker_state`
log record (`backend`, `from`, `to`), which is what detection and recovery
times are measured from. An attempt's outcome counts only while the breaker
is still in the state that admitted it: half-open is decided by its own
probes alone, a request admitted before the breaker opened changes nothing
when it ends, and the window after recovery holds only attempts admitted
since the breaker closed. On an
upstream error, after any retries it allows, or an open breaker, the route's
fallback backends are entered in order — at most once each, same model
contract; streams retry and fall back only before the first byte. A started
stream feeds its backend's breaker when it
ends: a mid-stream failure or a missing `[DONE]` as a failure, a completed
stream as a success, a client that leaves as nothing. A provider's rejection
of the request (400, 413, 422) ends the chain without counting as a failure,
and so does a client that leaves. Failover is observable per request: every
cost event records `attempts` (0 when no backend was tried), `retries`, and
`served_provider` when a backend answered. Backends come from
`PUSAQ_UPSTREAMS="name=url,..."` (first = primary, rest = fallbacks).

### Retries

Retries are off by default (`PUSAQ_RETRY_MAX=0`). With a maximum set, a
failure the same backend may answer differently is retried there before the
request falls back: a transport failure before the upstream answered
(connection refused or reset, DNS or TLS failure, a timeout before the
response headers) or status 408, 429, 500, 502, 503 or 504. Rejections
(400, 413, 422), other statuses, a 200 without a completion, a buffered body
that fails after its headers, and a client that leaves are never retried.
Bounds:

- **Per request:** at most `PUSAQ_RETRY_MAX` retries across the whole chain,
  so at most `backends + max` attempts.
- **Per instance:** a retry budget every request's first attempt credits with
  `PUSAQ_RETRY_BUDGET_RATIO` (default 0.2), refilled at one retry per second
  up to 10, one retry per withdrawal. Over T seconds and N requests an
  instance makes at most 10 + T + ratio · N retries, so a failing upstream
  sees at most (1 + ratio) times its first-attempt load plus the floor. A
  retry the budget refuses sends the request to its next backend at once.
- **In time:** before the n-th retry of a backend the request waits a
  uniform draw under min(2 s, 200 ms · 2ⁿ⁻¹), or the upstream's
  `Retry-After` when positive; the retry starts only if that wait ends within
  `PUSAQ_RETRY_WINDOW` (default 10 s) of the request's first upstream
  attempt, or before the request context's deadline. A longer wait sends the
  request to its next backend at once.

Every retry asks the backend's breaker for its own admission after the wait
and feeds its own outcome, so an open breaker stops retries on that backend,
and a failing request feeds the breaker up to `1 + max` failures. A retry
taken from the budget and then refused by the breaker or abandoned by the
client is returned to it. The token charge is made once before the chain and
reconciled once, whatever the retries. Cost events carry `retries`
(`attempts − retries` backends were attempted) and `retry_denied` naming
`budget` or `deadline` when a retryable failure was not retried for that
reason. A retried failure logs `backend failed, retrying` with its wait;
`backend failed, trying next` marks the failure that moved the request on.

### Idempotency key

Every upstream attempt carries `Idempotency-Key: pusaq-<request id>`, the
same on the retries and fallbacks of one request; the request ID is the one
returned in `X-Pusaq-Request-Id`, generated by the gateway. A client-sent
`Idempotency-Key` is neither forwarded nor folded in, so every client request
is a new upstream request. An upstream that deduplicates by this header can
recognize the gateway's retries; one that ignores it treats every attempt as
new and may bill each. Go's HTTP client treats a request carrying the header
as replayable and resends it over another connection when a reused idle
connection fails before the first response byte; such a resend is not an
attempt in the event.

## Semantic cache (shadow mode)

`PUSAQ_CACHE_MODE=shadow` turns on the
measurement instrument: eligible completions are evaluated
asynchronously — embed, nearest-neighbor within a namespace
(tenant | route's physical model | provider-reported model | system prompt |
temperature | `max_tokens` | rules version), record, insert — without ever
serving a cached answer. Bounded workers process samples independently of
the provider response; saturated intake drops and counts samples.
`cache_shadow` records are emitted when a neighbor exists and carry
`best_similarity` plus both response fingerprints. Different fingerprints
identify candidates for offline equivalence review; they do not prove a
semantic false positive. `PUSAQ_CACHE_TAU`
(default 0.95) only labels records. Embedders: `local` (deterministic,
$0 — measures the machinery, not paraphrase quality) or
`openai:<url>` + `PUSAQ_EMBED_MODEL` (and `PUSAQ_EMBED_KEY`) for real
embeddings from any OpenAI-compatible endpoint, independent of the chat
upstreams. Published
numbers must say which embedder produced them. Eligibility is decided once
per request and the reason a request was kept out travels on its cost
event as `cache_skip`: `tenant_off` (policy `cache=off`), `multi_turn`,
`sensitive`, `injection`, `temporal` (content rules, below), or
`temperature` (an explicit value above 0.7); an omitted temperature is
eligible under its own key.

The standalone default is `off`; Compose explicitly enables `shadow`.
Namespaces also contain fixed `tools=none` and `format=none` markers. The
reported model is read from the completed answer, buffered body or stream
chunks; when the provider reports none, the route's physical model takes its
place. Language is not a namespace field, and a model version change shows
only when the provider's reported name carries it.
The cache remains a measurement instrument and provides no serving guarantee.

The index retains vectors, IDs, namespaces and fingerprints without raw
prompts or responses. It has a 10,000-entry LRU cap and a separate 64 MiB
retained-data cap by default. Queued and active samples temporarily retain
eligible prompts for embedding within a shared 16 MiB budget, eight workers
and a 256-item queue. An external embedder receives that eligible prompt.
Evaluations have a 5 s context budget. Oversized index entries are skipped;
saturated intake and skipped evaluations are counted at shutdown. These data
caps exclude allocator and collection metadata and do not bound process RSS.

### Calibrating τ

The threshold is never a judgment call: `make calibrate`
sweeps a labeled pair dataset (equivalent vs trap pairs) with the same
embedder implementations the gateway runs, writes the precision/recall
curve to the ignored `out/calibration.csv`, and prints τ\*, the smallest τ whose one-sided Clopper–Pearson lower
bound on precision reaches 0.99 at 95% confidence (`cachecal -floor`,
`-confidence`). The bound needs support: with no false hit, at least 299
predicted hits; a τ whose observed precision is 1.0 from a handful of hits
does not qualify. Each curve row carries its predicted hits, false hits,
precision, lower bound, recall and F1; a ratio without a denominator, such
as precision at a τ with no hit, is written `undefined`, never 0. The
command fails loudly when the embedder cannot separate the traps (which the
local embedder, being lexical, often cannot: that failure is the harness
working) or when the best τ lacks support, and says which. The bound holds
per τ and for the dataset's pairs, not family-wise over the sweep nor for
production traffic. The seed dataset (60 pairs, 30 equivalent) exercises the
harness and cannot certify a τ; it is not a production corpus, and
`PUSAQ_CACHE_TAU` only labels the shadow records.

### Replaying labeled traffic

`make replay` answers what the cache would have done on a stream of
traffic. It samples seeded Zipf traffic over a labeled EN/ES corpus
(`testdata/replay/intents.csv`: intents with paraphrases, grouped with
look-alike intents that need different answers) and runs every request
through the shadow's own code: the namespace derivation, the content rules
for eligibility, and `Shadow.Evaluate` (embed, nearest neighbor, record,
insert after the lookup). A request is a predicted hit at τ under the rule
that labels `shadow_hit`; a hit is false when the neighbor carries a
different labeled answer, including the same intent in the other language.
For the configured τ and a sweep it reports the hit-rate over eligible
requests next to the false-positive rate (false hits over hits), net savings
(provider cost avoided by correct hits minus the embedding toll on every
eligible request; the spend that false hits skip is reported apart), latency
p50/p95 with and without the cache from the mock provider's latency model,
and the break-even hit-rate. Request count, Zipf exponent, seed, τ, prices,
answer length and index caps are flags of `cmd/cachereplay`; outputs go to
the ignored `out/replay/`. The [replay report](measurements/cache-replay.md)
records a run and its limits: with the lexical local embedder the hit-rate is
almost entirely exact repeats, and at τ = 0.95 every hit on a text not seen
before is a trap.

## Tenant policies

`PUSAQ_TENANT_POLICIES="tenant:key=value,...;tenant2:..."`
gives a tenant up to seven policies: `models=pusaq/fast|pusaq/best` (the
concrete aliases it may be served by — `pusaq/auto` is checked against the
alias it resolves to and denied rather than rerouted), `max_prompt_chars`
(bytes of message content), `budget_usd` (monthly ceiling; spend is
counted in Redis from `cost_usd` per completion, estimated ones included,
read before token reservation and provider invocation, and
reset at the UTC month boundary — requests already in flight when the
budget is reached may complete, so the overrun is bounded by their cost),
`fail_closed=true` (reject with 503 instead of admitting when the store
is down), `sensitive=log|block` and `injection=log|block` (what a content
rule match does: flag the event, the default, or answer 403), and
`cache=on|off` (keep the tenant out of the semantic cache). Policies are
enforced after the alias is resolved and before TPM reservation or provider
invocation. RPM admission has already counted the request. Tenants without
an entry are unrestricted.
Every entry is validated at startup; a budget needs `PUSAQ_PRICES` and
`PUSAQ_REDIS_ADDR`. Budgeted tenants' cost events carry `spend_usd_month`.
A request that a provider answered without usage (a stream without a usage
chunk, a client that left mid-stream, a mid-stream failure) is priced from
its reservation — the input estimate plus `max_tokens` or
`PUSAQ_TPM_RESERVE_DEFAULT` — and its event carries `cost_estimated`.

### Content rules

Every request's messages are classified by the policy
filter's content rules — personal data and secrets (`pii:email`,
`pii:phone`, `pii:card` with Luhn, `pii:iban` with mod-97, `secret:*`),
instruction-override phrasing in English and Spanish (`injection:*`), and
time-dependence cues (`temporal:*`) — and the kinds matched go on the cost
event as `policy_flags`, never the content. Sensitive data is scanned in
every message; injection and temporal rules skip the system prompt, where
applications legitimately set personas and today's date. A match keeps the
prompt out of the cache and, when the tenant's policy says `block`, denies
the request with 403 `policy_denied` naming the kinds. The rules are regex
and keyword heuristics with stated limits, measured on a labeled corpus
([content-rules.md](measurements/content-rules.md)); the suite fails
below the published precision/recall floors.

## Upstreams

An upstream is any OpenAI-compatible server. `PUSAQ_UPSTREAMS` lists them in
priority order (`name=url,…`); the first is the primary for every alias and
the rest are fallbacks. Each URL is the API base with its version segment,
as OpenAI SDKs take it (`http://host:9090/v1`, `https://host/v1beta/openai`):
completions go to `POST <url>/chat/completions`, buffered or streamed. The
shadow cache's `openai:<url>` embedder has its own base URL and calls
`POST <url>/embeddings`. `PUSAQ_ROUTES` maps each alias to the physical
model sent upstream. `PUSAQ_UPSTREAM_KEY_<NAME>` (the upper-cased upstream
name, with non-alphanumeric characters replaced by `_`) is the bearer key
for that upstream and is never logged.

Gateway configuration accepts only absolute HTTP/HTTPS bases without URL
userinfo, queries or fragments. Provider and embedding API keys require
HTTPS; explicit development mode permits keyed HTTP only on localhost or
literal loopback addresses. Keyless HTTP mocks are supported. Both clients
disable redirects, including 307 and 308, so a configured endpoint cannot
redirect credentials or request payloads to another destination.

## Not implemented

Serving answers from the cache; rewriting or masking flagged content (prompt
text is never logged and flagged prompts never reach the cache); scanning
responses for secrets; tool calling and response formats; and a standing
deployment (the two-region runtime
module exists for measurement runs only and is destroyed after each one).
