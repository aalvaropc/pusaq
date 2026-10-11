# Gateway reference

How the gateway behaves, mechanism by mechanism. Every variable named here is listed in [configuration.md](configuration.md); the measurements that back the claims are in [measurements/](measurements/).

## API

`POST /v1/chat/completions` — OpenAI request subset (`model`, `messages`,
`max_tokens`, `temperature`, `stream`, `metadata`). `temperature` is
forwarded to the provider exactly as sent (an explicit 0 included) and is
part of the cache key. With `"stream": true` the response is Server-Sent
Events: `chat.completion.chunk` deltas passed through from the provider,
a final usage chunk, then `data: [DONE]`. The `model` field takes an alias:
`pusaq/fast` and `pusaq/best` resolve to physical models in configuration;
`pusaq/auto` resolves through routing rules keyed by the request's
`metadata.feature_tag` (`PUSAQ_ROUTING_RULES`, default
`classify`/`extract` → fast, `reason`/`code` → best, everything else →
fast). Responses carry `X-Pusaq-Model` (the physical model served),
`X-Pusaq-Route-Reason` (`direct`, `rule:<tag>`, `default:untagged`, or
`default:unknown_tag`), `X-Pusaq-Request-Id`, and — when the replica is
labeled with `PUSAQ_REGION` — `X-Pusaq-Region`; the cost event records the
same as `route_reason`, `feature_tag`, and `region`. A request may declare
`metadata.run_id` (a string of at most 64 characters), read before
admission and recorded on its cost event as `run_id` — also when the rate
limiter rejects it — so a measurement run can be read back from the events.
All errors share one JSON shape: `{"error":{"code":"...","message":"..."}}`
— provider failures surface as `upstream_error` (502) without leaking
upstream responses; a tenant policy denial is `policy_denied` (403); an
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
the gateway **fails open** within a 250ms budget — temporarily inexact
limits beat an outage caused by the limiter itself — unless the tenant's
policy sets `fail_closed`, in which case it answers 503 `store_unavailable`.
Unset `PUSAQ_REDIS_ADDR` disables limiting with a startup warning.

Two modes, chosen with `PUSAQ_RATELIMIT_MODE`. **`strict`** (default): one
atomic store decision per request, exact across replicas. **`lease`**: each
replica takes up to `PUSAQ_LEASE_SIZE` request tokens per round-trip and
admits locally; unused tokens return when the lease passes `PUSAQ_LEASE_TTL`
or the replica drains, and a store denial is cached locally until its retry
time. Cumulative admissions never exceed the bucket in either mode; in lease
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
on success, on error, and at stream end. Unknown usage keeps the charge.
Every cost event records `tokens_charged` next to actual usage, so the
estimate's error is measurable from the event stream. Under overload the
tokens actually consumed stay within the bucket over the window; the peak
excess is bounded by the reservation error of the requests admitted before
reconciliation caught up (measured, see Measurements).

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
before the first byte. Failover is observable per request: every cost event
records `served_provider` and `attempts`. Backends come from
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
`openai:<upstream>` + `PUSAQ_EMBED_MODEL` for real embeddings. Published
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
dataset (60 pairs) proves the harness; a production τ needs a
production-traffic corpus (≥200+200 pairs) and a real embedder.

### Authentication

Configure tenants with `PUSAQ_API_KEYS="key:tenant,..."`;
clients send `Authorization: Bearer <key>`, and every cost event carries the
tenant. Missing/unknown keys get a normalized `unauthorized` (401). With no
keys configured the gateway runs in **dev mode** — no auth, everything
attributed to the `dev` tenant, and a warning logged at startup.
`/healthz` and `/readyz` never require credentials.

### Tenant policies

`PUSAQ_TENANT_POLICIES="tenant:key=value,...;tenant2:..."`
gives a tenant up to seven policies: `models=pusaq/fast|pusaq/best` (the
concrete aliases it may be served by — `pusaq/auto` is checked against the
alias it resolves to and denied rather than rerouted), `max_prompt_chars`
(bytes of message content), `budget_usd` (monthly ceiling; spend is
counted in Redis from `cost_usd` per completion, read before admission, and
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

## Serving a real model (Gemini)

Google exposes Gemini on an OpenAI-compatible endpoint, so a real model is
a config entry — no code:

```sh
export PUSAQ_UPSTREAMS="gemini=https://generativelanguage.googleapis.com/v1beta/openai,mock=http://localhost:9090"
export PUSAQ_UPSTREAM_KEY_GEMINI="<your Gemini API key>"
export PUSAQ_ROUTES="pusaq/fast=gemini-2.5-flash,pusaq/best=gemini-2.5-pro"
export PUSAQ_ROUTING_RULES="classify=pusaq/fast,extract=pusaq/fast,reason=pusaq/best,code=pusaq/best,default=pusaq/fast"
make run
```

Requests to `pusaq/fast` now hit Gemini through the whole pipeline —
limits, breakers, accounting — with the mock as the fallback backend.
Upstream keys come from `PUSAQ_UPSTREAM_KEY_<NAME>` (uppercased upstream
name) and are never logged.

## Not implemented

Serving answers from the cache; rewriting or masking flagged content (the
gateway never logs prompt text and the cache never sees flagged prompts, so
there is nothing to mask yet); outbound secret scanning of responses; and a
standing multi-region deployment (the two-region runtime module exists for
measurement runs only and is destroyed after each one).
