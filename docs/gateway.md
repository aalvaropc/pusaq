# Gateway reference

How the gateway behaves, mechanism by mechanism. Every variable named here is listed in [configuration.md](configuration.md); the measurements that back the claims are in [measurements/](measurements/).

## API

`POST /v1/chat/completions` — OpenAI request subset (`model`, `messages`,
`max_tokens`, `temperature`, `stream`, `metadata`). `max_tokens`, when
present, is a positive integer. `temperature` is forwarded to the provider
exactly as sent (an explicit 0 included) and is part of the cache key.
Fields that would change the response contract — `tools`, `tool_choice`,
`response_format`, `n` other than 1, `logprobs`, `audio`, message
`tool_calls`/`tool_call_id`, content given as an array of parts, and the
rest of that family — are rejected with 400 `invalid_request` naming the
field; other fields are ignored, and a field set to `null` or `false`
counts as absent. With `"stream": true` the response is Server-Sent
Events: `chat.completion.chunk` deltas passed through from the provider,
a final usage chunk (requested upstream with `stream_options.include_usage`),
then `data: [DONE]`. A stream completes only with `[DONE]`: when the provider
fails mid-stream, ends without `[DONE]`, or sends an event over 1 MiB, the
stream ends with one event carrying the error shape below with code
`upstream_error`, and no `[DONE]`. The `model` field takes an alias:
`pusaq/fast` and `pusaq/best` resolve to physical models in configuration;
`pusaq/auto` resolves through routing rules keyed by the request's
`metadata.feature_tag` (`PUSAQ_ROUTING_RULES`, default
`classify`/`extract` → fast, `reason`/`code` → best, everything else →
fast). Responses carry `X-Pusaq-Model` (the physical model served),
`X-Pusaq-Route-Reason` (`direct`, `rule:<tag>`, `default:untagged`, or
`default:unknown_tag`), `X-Pusaq-Request-Id`, and — when the replica is
labeled with `PUSAQ_REGION` — `X-Pusaq-Region`; the cost event records the
same as `route_reason`, `feature_tag`, and `region`. A feature tag is at
most 64 letters, digits, `.`, `_` or `-`; anything else is 400. A request may declare
`metadata.run_id` (a string of at most 64 characters), read before
admission and recorded on its cost event as `run_id` — also when the rate
limiter rejects it — so a measurement run can be read back from the events.
All errors share one JSON shape: `{"error":{"code":"...","message":"..."}}`
— provider failures surface as `upstream_error` (502) and a provider's
rejection of the request (400, 413, 422) as `invalid_request` (400), neither
leaking upstream responses; a request whose client left before any answer is
recorded as `client_canceled`; a tenant policy denial is `policy_denied` (403); an
exhausted monthly budget is `budget_exceeded` (402, with `retry_after_ms`
until the next UTC month); a store outage for a fail-closed tenant is
`store_unavailable` (503); a replica inside an injected not-ready window
answers `not_ready` (503, with `Retry-After`).

### Rate limiting

With `PUSAQ_REDIS_ADDR` set, each tenant gets a token
bucket in Redis decided by one atomic Lua script shared by every replica —
read-decide-decrement as a single operation, so the limit stays exact under
concurrency (proven by test: exactly the bucket capacity admitted out of 200
concurrent requests across two instances). Denials are 429 `rate_limited`
with `retry_after_ms` and a `Retry-After` header. If Redis is unreachable
the gateway **fails open** within a 250 ms budget per store call —
temporarily inexact limits beat an outage caused by the limiter itself —
unless the tenant's policy sets `fail_closed`, in which case it answers 503
`store_unavailable`.
Unset `PUSAQ_REDIS_ADDR` disables limiting with a startup warning.

Two modes, chosen with `PUSAQ_RATELIMIT_MODE`. **`strict`** (default): one
atomic store decision per request, exact across replicas. **`lease`**: each
replica takes up to `PUSAQ_LEASE_SIZE` request tokens per round-trip and
admits locally; unused tokens return when the lease passes `PUSAQ_LEASE_TTL`
or the replica drains, and a store denial is cached locally until its retry
time or the lease TTL, whichever is sooner. Cleanup revalidates entry ownership
and never waits for a tenant while holding the shared tenant-map mutex. Cumulative admissions never exceed the bucket in either mode; in lease
mode up to `replicas × lease size` tokens can sit unspent in leases at any
moment, and a replica that dies without draining strands at most one lease
per tenant (under-admission, never overrun). Lease mode covers request
limits only; token limits are always strict. Both modes are measured with
the same load test.

### Token limits (TPM)

Alongside RPM, each tenant has an LLM-token bucket.
After model resolution a request is charged its input estimate (chars/4)
plus an output reservation (`max_tokens`, or `PUSAQ_TPM_RESERVE_DEFAULT`);
when actual usage is known the difference is reconciled back atomically —
on success, on error, and at stream end. A confirmed reservation is fully
returned if no provider was invoked. After any provider invocation, unknown
usage keeps the charge, including errors, timeouts and abandoned streams.
Every cost event records `tokens_charged` next to actual usage, so the
estimate's error is measurable from the event stream. Under overload the
tokens actually consumed stay within the bucket over the window; the peak
excess is bounded by the reservation error of the requests admitted before
reconciliation caught up (measured, see Measurements).

Refill uses Redis time inside each atomic Lua operation, with a timestamp
that never moves backward. A stored timestamp ahead of Redis time pauses
refill until catch-up; retry times include that delay. Buckets retain their
keys and balances across the change. This requires Redis 7 or newer; gateways
sharing a bucket use the same implementation.

### Resilience

Each upstream backend is guarded by a circuit breaker
(closed / open / half-open). Defaults: opens on a >50% failure rate over a
20-sample window with a 10-sample minimum, 10s cooldown, 3 half-open
probes — configurable with `PUSAQ_BREAKER_WINDOW`, `PUSAQ_BREAKER_MIN_SAMPLES`,
`PUSAQ_BREAKER_THRESHOLD`, `PUSAQ_BREAKER_COOLDOWN`, `PUSAQ_BREAKER_PROBES`,
validated and logged at startup. Every transition is one `breaker_state`
log record (`backend`, `from`, `to`), which is what detection and recovery
times are measured from. On an
upstream error or an open breaker, the route's fallback backends are tried
in order — at most once each, same model contract; streams fall back only
before the first byte. A started stream feeds its backend's breaker when it
ends: a mid-stream failure or a missing `[DONE]` as a failure, a completed
stream as a success, a client that leaves as nothing. A provider's rejection
of the request (400, 413, 422) ends the chain without counting as a failure,
and so does a client that leaves. Failover is observable per request: every
cost event records `served_provider` and `attempts`. Backends come from
`PUSAQ_UPSTREAMS="name=url,..."` (first = primary, rest = fallbacks).

### Semantic cache (shadow mode)

`PUSAQ_CACHE_MODE=shadow` turns on the
measurement instrument: eligible completions are evaluated
asynchronously — embed, nearest-neighbor within an exhaustive namespace
(tenant | served model | system prompt | temperature | rules version),
record, insert — without ever
serving a cached answer and without touching request latency (bounded
workers; saturation drops are counted). Every `cache_shadow` record carries
`best_similarity` plus both response fingerprints, so precision/recall for
any τ is computable offline from the log stream alone; `PUSAQ_CACHE_TAU`
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

### Calibrating τ

The threshold is never a judgment call: `make calibrate`
sweeps a labeled pair dataset (equivalent vs trap pairs) with the same
embedder implementations the gateway runs, writes the precision/recall
curve, and prints `τ* = min{τ : precision ≥ 0.99}` — or fails loudly when
the embedder cannot separate the traps (which the local embedder, being
lexical, often cannot: that failure is the harness working). The seed
dataset (60 pairs) exercises the harness; it is not a production corpus,
and `PUSAQ_CACHE_TAU` only labels the shadow records.

### Authentication

Configure tenants with `PUSAQ_API_KEYS="key:tenant,..."`;
clients send `Authorization: Bearer <key>`, and every cost event carries the
tenant. Missing/unknown keys get a normalized `unauthorized` (401). With
`PUSAQ_DEV_MODE=true` and `PUSAQ_API_KEYS` unset the gateway runs in **dev mode** — no auth,
everything attributed to the `dev` tenant, and a warning logged at startup;
absent both settings or set but empty, it refuses to start. Development mode
and configured API keys cannot be combined. Configuration errors name positions and
tenants, never a key.
`/healthz` and `/readyz` never require credentials.

### Tenant policies

`PUSAQ_TENANT_POLICIES="tenant:key=value,...;tenant2:..."`
gives a tenant up to seven policies: `models=pusaq/fast|pusaq/best` (the
concrete aliases it may be served by — `pusaq/auto` is checked against the
alias it resolves to and denied rather than rerouted), `max_prompt_chars`
(bytes of message content), `budget_usd` (monthly ceiling; spend is
counted in Redis from `cost_usd` per completion, estimated ones included,
read before admission, and
reset at the UTC month boundary — requests already in flight when the
budget is reached may complete, so the overrun is bounded by their cost),
`fail_closed=true` (reject with 503 instead of admitting when the store
is down), `sensitive=log|block` and `injection=log|block` (what a content
rule match does: flag the event, the default, or answer 403), and
`cache=on|off` (keep the tenant out of the semantic cache). Policies are
enforced after the alias is resolved and before any token is charged, so a
denied request costs nothing. Tenants without an entry are unrestricted.
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
name) is the bearer key for that upstream and is never logged.

## Not implemented

Serving answers from the cache; rewriting or masking flagged content (prompt
text is never logged and flagged prompts never reach the cache); scanning
responses for secrets; tool calling and response formats; and a standing
deployment (the two-region runtime
module exists for measurement runs only and is destroyed after each one).
