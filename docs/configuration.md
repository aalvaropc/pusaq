# Configuration

The gateway reads environment variables; it does not load configuration
files or `.env` itself. Recognized settings are parsed at startup, and an
invalid setting stops the process. Mechanisms are explained in
[gateway.md](gateway.md); setup and publication in [operations.md](operations.md).

The tables below describe **standalone binary defaults**. Docker Compose
sets explicit overrides, listed at the end of this document. Duration
settings use Go syntax such as `500ms`, `10s` and `1m`.

## Server

| Variable | Default | What it does |
|---|---|---|
| `PUSAQ_ADDR` | `:8080` | Listen address; the standalone default binds all interfaces. Use `127.0.0.1:8080` for a host-only local process. |
| `PUSAQ_DRAIN_DELAY` | `1s` | On SIGTERM, keep serving this long after turning not-ready so balancer probes see the 503. |
| `PUSAQ_SHUTDOWN_GRACE` | `10s` | How long in-flight requests get to finish after the drain delay. |
| `PUSAQ_TELEMETRY_BUFFER` | `1024` | Cost-event buffer; overflow drops events and counts them. |
| `PUSAQ_BODY_READ_TIMEOUT` | `10s` | Positive deadline for reading an authenticated request body. |
| `PUSAQ_WRITE_IDLE_TIMEOUT` | `30s` | Positive deadline per response write/flush; no total SSE lifetime limit. |
| `PUSAQ_REGION` | unset | Region label on every event and on `X-Pusaq-Region` (letters, digits, `.`, `_`, `-`). |
| `PUSAQ_ADMIN_TOKEN` | unset | Enables `POST /admin/readiness` (failure injection); at least 16 characters, no whitespace. Unset means no admin endpoints. |

The request-body cap is fixed at 1 MiB. Header reads have a fixed 10 s
timeout; idle HTTP connections have a fixed 120 s timeout. SSE has a fixed
1 MiB event-framing cap. These are not environment settings.

## Upstreams and routing

| Variable | Default | What it does |
|---|---|---|
| `PUSAQ_UPSTREAMS` | `mock=http://localhost:9090/v1` | Backends in priority order, `name=url,...`; the first is primary, the rest are fallbacks. Each URL is the API base with its version segment; completions go to `<url>/chat/completions`. |
| `PUSAQ_UPSTREAM_KEY_<NAME>` | unset | API key for upstream `<NAME>`: the name upper-cased, every character other than a letter or digit as `_`; never logged. Two upstreams that map to the same variable fail startup. |
| `PUSAQ_UPSTREAM_TIMEOUT` | `30s` | Bound on every provider call: the whole exchange when buffered; the connection, TLS handshake, response headers and every silence between chunks when streamed. |
| `PUSAQ_ROUTES` | both aliases → `mock-model` | `alias=model,...` for `pusaq/fast` and `pusaq/best`. |
| `PUSAQ_ROUTING_RULES` | `classify,extract`→fast; `reason,code`→best; default fast | `tag=alias,...,default=alias`; replaces the table `pusaq/auto` resolves through. Tags use letters, digits, `.`, `_` and `-`. |

## Tenants and policies

| Variable | Default | What it does |
|---|---|---|
| `PUSAQ_DEV_MODE` | `false` | Explicit unauthenticated development; requires API keys to be unset. Requests belong to `dev`. |
| `PUSAQ_API_KEYS` | required outside development mode | `key:tenant,...`, spaces around fields ignored. Missing outside development mode or set but empty fails startup. |
| `PUSAQ_TENANT_POLICIES` | unset | `tenant:key=value,...;tenant2:...` with `models`, `max_prompt_chars`, `budget_usd`, `fail_closed`, `sensitive`, `injection`, `cache`. A budget needs `PUSAQ_PRICES` and `PUSAQ_REDIS_ADDR`. |
| `PUSAQ_PRICES` | unset (no cost fields) | `model=usd_per_1M_input/usd_per_1M_output,...` keyed by physical model; every route model must be priced. |
| `PUSAQ_PRICE_TABLE_VERSION` | required with prices | Label carried on every priced event as `price_table_version`. |

## Rate limiting

| Variable | Default | What it does |
|---|---|---|
| `PUSAQ_REDIS_ADDR` | unset (limiting off, with a warning) | Redis for buckets and spend counters. |
| `PUSAQ_RPM_LIMIT` | `60` | Requests per minute per tenant. |
| `PUSAQ_RPM_BURST` | = limit | Request bucket capacity. |
| `PUSAQ_RATELIMIT_MODE` | `strict` | `strict` (one atomic decision per request) or `lease` (batches of tokens per replica). |
| `PUSAQ_LEASE_SIZE` | `10` | Tokens per lease; at most the burst in lease mode. |
| `PUSAQ_LEASE_TTL` | `1s` | How long a replica may hold unused lease tokens. |
| `PUSAQ_TPM_LIMIT` | `100000` | LLM tokens per minute per tenant; `0` disables token limiting (with a warning). |
| `PUSAQ_TPM_BURST` | = limit | Token bucket capacity; an empty value also means the default. |
| `PUSAQ_TPM_RESERVE_DEFAULT` | `512` | Positive output reservation when `max_tokens` is absent, also used to price a request answered without usage; at most `PUSAQ_TPM_BURST` while token limiting is on. A prompt estimate plus output reservation above the burst is rejected with 400. |

Buckets expire after 10 minutes of inactivity. RPM and TPM require
`PUSAQ_REDIS_ADDR`; no Redis address disables both. A store call has a 250 ms
context budget. Store errors fail open unless the tenant's policy sets
`fail_closed=true`. See [quota semantics](gateway.md#rate-limiting).

## Circuit breakers (per upstream backend)

| Variable | Default | What it does |
|---|---|---|
| `PUSAQ_BREAKER_WINDOW` | `20` | Sliding window of recent outcomes. |
| `PUSAQ_BREAKER_MIN_SAMPLES` | `10` | Minimum samples before the breaker may open. |
| `PUSAQ_BREAKER_THRESHOLD` | `0.5` | Failure rate that opens it. |
| `PUSAQ_BREAKER_COOLDOWN` | `10s` | Open time before half-open probes. |
| `PUSAQ_BREAKER_PROBES` | `3` | Probe requests allowed in half-open. |

## Semantic cache (shadow mode)

| Variable | Default | What it does |
|---|---|---|
| `PUSAQ_CACHE_MODE` | `off` | `off` or `shadow`. Shadow measures and never serves. |
| `PUSAQ_CACHE_TAU` | `0.95` | Similarity threshold that labels shadow records hit/miss. |
| `PUSAQ_CACHE_MAX_BYTES` | `67108864` | Retained index vectors and string data; raw prompts are excluded. Metadata overhead is outside this accounting. |
| `PUSAQ_CACHE_QUEUE_MAX_BYTES` | `16777216` | Retained sample data across queued and in-progress evaluations. Positive byte count. Saturation drops samples. |
| `PUSAQ_CACHE_MAX_ENTRIES` | `10000` | Cap on the in-process index (LRU). |
| `PUSAQ_EMBEDDER` | `local` | `local` or `openai:<url>`, an OpenAI-compatible API base with its version segment; embeddings go to `<url>/embeddings`. Independent of `PUSAQ_UPSTREAMS`. |
| `PUSAQ_EMBED_MODEL` | unset | Embedding model; required with an `openai:` embedder. |
| `PUSAQ_EMBED_KEY` | unset | Bearer key for the `openai:` embedder; never logged. Set without an `openai:` embedder, startup fails. |

## Outbound endpoint policy

Gateway provider and embedding bases accept only absolute HTTP/HTTPS URLs
without user information, query strings, or fragments. API keys require HTTPS;
explicit development mode allows keyed HTTP only for localhost or literal
loopback IPs. Keyless HTTP mocks remain supported. Neither client follows
redirects, including 307 and 308. Versioned base paths are preserved.

These checks apply to provider and embedding credentials, independently of
the tenant keys accepted on incoming requests.

## Local stack overrides

The default [Compose service](../docker-compose.yml) sets:

| Setting | Compose value |
|---|---|
| `PUSAQ_DEV_MODE` | `true`; no tenant API keys |
| `PUSAQ_REDIS_ADDR` | `redis:6379` |
| `PUSAQ_UPSTREAMS` | Primary `mockprovider` and fallback `mockbackup`, both keyless HTTP mocks |
| `PUSAQ_RPM_LIMIT` / `PUSAQ_RPM_BURST` | `60` / `50` |
| `PUSAQ_TPM_LIMIT` / `PUSAQ_TPM_RESERVE_DEFAULT` | `300` / `50` |
| `PUSAQ_CACHE_MODE` | `shadow` with the local embedder |
| `PUSAQ_PRICES` / `PUSAQ_PRICE_TABLE_VERSION` | Synthetic mock prices and a local version label |
| `PUSAQ_DRAIN_DELAY` | `0s` |

Published ports bind to `127.0.0.1`. The `loadtest` profile has its own keys,
quotas and disabled shadow sampling. `make run` explicitly sets development
mode; it does not inherit the other Compose service settings. Inspect the
effective stack with `docker compose config`.
