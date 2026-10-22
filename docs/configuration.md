# Configuration

The gateway reads environment variables only (no files). Every value is validated at startup; an
invalid one stops the process with an error naming the variable. Mechanisms are explained in
[gateway.md](gateway.md); deployment in [operations.md](operations.md).

## Server

| Variable | Default | What it does |
|---|---|---|
| `PUSAQ_ADDR` | `:8080` | Listen address. |
| `PUSAQ_DRAIN_DELAY` | `1s` | On SIGTERM, keep serving this long after turning not-ready so balancer probes see the 503. |
| `PUSAQ_SHUTDOWN_GRACE` | `10s` | How long in-flight requests get to finish after the drain delay. |
| `PUSAQ_TELEMETRY_BUFFER` | `1024` | Cost-event buffer; overflow drops events and counts them. |
| `PUSAQ_BODY_READ_TIMEOUT` | `10s` | Positive deadline for reading an authenticated request body. |
| `PUSAQ_WRITE_IDLE_TIMEOUT` | `30s` | Positive deadline per response write/flush; no total SSE lifetime limit. |
| `PUSAQ_REGION` | unset | Region label on every event and on `X-Pusaq-Region` (letters, digits, `.`, `_`, `-`). |
| `PUSAQ_ADMIN_TOKEN` | unset | Enables `POST /admin/readiness` (failure injection); at least 16 characters, no whitespace. Unset means no admin endpoints. |

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
| `PUSAQ_TPM_RESERVE_DEFAULT` | `512` | Output reservation when a request sets no `max_tokens`, also used to price a request answered without usage; at most `PUSAQ_TPM_BURST` while token limiting is on. A request whose prompt estimate plus `max_tokens` exceeds the burst is rejected with 400. |

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
