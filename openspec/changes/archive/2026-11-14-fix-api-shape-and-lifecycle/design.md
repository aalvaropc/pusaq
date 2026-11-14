# Design: fix-api-shape-and-lifecycle

## Context

The HTTP adapter (`internal/gateway/server.go`) registers its routes on a `http.ServeMux` and renders pipeline errors through `writeError`; anything the mux rejects itself is Go's plain text. Bearer parsing is `strings.CutPrefix(h, "Bearer ")` in two places. The not-ready window check runs at the top of the pipeline handler, before `Accounting`, so the refusal has no request ID and no cost event. The bucket TTL is a constant passed to every Lua script as `EXPIRE` seconds. `cmd/pusaq/main.go` holds all wiring and the drain sequence in `run()`. See proposal.md.

## Goals / Non-Goals

**Goals:**

- Every error response, from the pipeline or not, has one JSON shape, OpenAI's `type` and `param` included, and a request ID.
- Authentication accepts what RFC 9110 allows for the scheme name.
- A not-ready refusal is correlated like any other rejection, without changing the fixed limit ordering.
- Bucket expiry never changes an admission decision.
- An operator can force an exit during a drain.
- One slow tenant cannot spend the refund budget of the others.
- The composition root is tested.

**Non-Goals:**

- New OpenAI fields beyond `created` and `system_fingerprint`, or new endpoints such as `/v1/models`.
- A `run_id` on not-ready refusals: the body is never read.
- A configurable bucket TTL or sweep budget.
- Flushing telemetry on a forced exit.

## Decisions

1. **Router errors through the mux.** For each registered `METHOD /path`, the adapter also registers the bare `/path` with a handler that answers 405 `method_not_allowed` and an `Allow` header listing that path's methods (`GET` implies `HEAD`, as the mux does); `/` answers 404 `not_found`. Go's precedence makes a method pattern beat the bare path and any path beat `/`, so the registered routes are unaffected. `method_not_allowed` is a new code mapped to 405.

2. **Request ID on every error.** `writeError` stamps the pipeline's request ID when there is one and a fresh ID otherwise (router errors, admin errors). Those fresh IDs reference no cost event; logging every 404 would give clients a log-flooding lever.

3. **Error `type` follows the HTTP status the way OpenAI SDKs classify errors.** The mapping lives next to the status mapping in `core`:

   | Code | Status | `type` |
   |---|---|---|
   | `invalid_request` | 400 | `invalid_request_error` |
   | `unauthorized` | 401 | `authentication_error` |
   | `budget_exceeded` | 402 | `insufficient_quota` |
   | `policy_denied` | 403 | `permission_error` |
   | `not_found` | 404 | `not_found_error` |
   | `method_not_allowed` | 405 | `invalid_request_error` |
   | `rate_limited` | 429 | `rate_limit_error` |
   | `client_canceled` | 499 | `invalid_request_error` |
   | `internal` | 500 | `server_error` |
   | `upstream_error` | 502 | `server_error` |
   | `store_unavailable` | 503 | `server_error` |
   | `not_ready` | 503 | `server_error` |

   `authentication_error`, `permission_error`, `not_found_error` and `rate_limit_error` match the SDK classes for 401, 403, 404 and 429; `server_error` is OpenAI's own type for its 5xx answers; other 4xx fall back to OpenAI's general `invalid_request_error`. `insufficient_quota` is the type OpenAI uses for an exhausted billing quota — the same meaning as `budget_exceeded`, which stays 402 so SDKs do not retry it. An unregistered code renders `server_error`, as it renders 500. `code` keeps carrying the precise reason.

4. **`param` names the field.** `core.Error` gains `Param`, rendered as `null` when empty. Validation errors set it to the field their message names, in the message's notation: `model`, `messages`, `max_tokens`, `messages[].content`, `messages[].tool_calls`, the unsupported top-level field, `metadata.feature_tag`, `metadata.run_id`, the JSON path of a mistyped field, and `state` or `for` on the admin endpoint. Errors about the whole body (invalid JSON, too large, unreadable), provider rejections, policy denials and the token-burst check have no single field and render `null`. The stream error event is built by the same encoder as `writeError`, so the two shapes cannot drift.

5. **`created` and `system_fingerprint`.** `provider.ChatResponse` decodes both. The buffered response orders fields as OpenAI does (`id`, `object`, `created`, `model`, `system_fingerprint`, `choices`, `usage`); `created` is the provider's value when positive and the gateway's Unix time when the response is built otherwise; `system_fingerprint` is omitted when empty. Streamed chunks are forwarded byte for byte and keep whatever the provider sent.

6. **Bearer parsing.** One helper splits the header at the first space or tab, compares the scheme with `strings.EqualFold`, and trims the rest; anything else counts as no bearer token. The API and the admin endpoint share it.

7. **Not-ready refusal after authentication.** The window check moves into the adapter's lazy payload reader. `Auth` calls that reader right after resolving the tenant, before `RateLimit`, so the pipeline order becomes `Accounting → RequestID → Auth → not-ready check → body read → RateLimit → … → TPM`. Consequences:
   - the refusal carries a request ID and exactly one cost event (`not_ready`, tenant, region, no run ID because the body is never read);
   - the tenant-auth rule "resolve the key before any other request processing" holds: a bad key gets 401 during a window, as outside it;
   - the refused request reads no body and charges neither the request bucket nor the token bucket — constitution rule 4 is untouched;
   - the chaos verifier selects cost events by `run_id`, so refusals stay outside its comparisons, as they were.

   Keeping the refusal ahead of the pipeline with only a fresh ID was rejected: the ID would reference nothing, and authentication would still not come first.

8. **TTL from the refill, computed in the script.** Every Lua script persists the bucket with `EXPIRE max(floor, ceil((rollback_ms + (B − tokens) / r × 1000) / 1000))`: the time the refill needs to bring the balance written now back to capacity, plus any wait for a store clock that is behind the bucket's timestamp. After that time an expired bucket and a kept one are indistinguishable, so reclaiming never changes a decision — including a negative balance left by reconciliation, which a fixed TTL based on `B / r` would forgive. `Limits.TTL` becomes the floor; `cmd/pusaq` passes one minute. The value is capped at 10¹² s, so an absurdly small rate cannot overflow `EXPIRE` and turn every call into a store error. The floor keeps the integer `EXPIRE` positive for sub-second refills and keeps a full idle bucket for a minute, as cheap as before. Computing the TTL inside the script keeps the decision and its expiry in one atomic operation (constitution rule 5).

9. **Second signal.** `serve` calls the `NotifyContext` stop function as soon as the first signal arrives. With no handler registered, the Go runtime applies the default action: a second SIGTERM or SIGINT terminates the process. In-flight requests are cut and buffered events can be lost; that is the purpose of a second signal.

10. **Per-call sweep budget.** `sweep` no longer takes a context; each `Adjust` gets `context.WithTimeout(context.Background(), sweepBudget)`. A sweep over n tenants with tokens to return can take up to n × 250 ms against a hung store; the janitor ticker drops ticks while a sweep runs, and `Close` takes at most 250 ms per such tenant.

11. **Composition root.** `run` loads the configuration, registers the signal context, builds an `app` with `newApp(cfg, logger)` and serves on a listener opened with `net.Listen` (what `ListenAndServe` does). `newApp` is the former wiring, split into helpers for policies, limits, shadow cache, upstreams and prices, and keeps every log record. `app.serve(ctx, stop, ln)` is the former select/drain/shutdown sequence; `app.close()` stops the lease limiter, the shadow cache and the emitter. Tests drive `newApp` from `config.Load` with miniredis and `httptest` upstreams, run the drain against a real SIGTERM delivered to the test process, and check the second signal in a child process, because the default action would end the test binary.

## Risks / Trade-offs

- Clients that parsed the plain-text 404/405 bodies see JSON. The status codes are unchanged.
- `type` values are a gateway convention modelled on OpenAI's, not a copy of every value OpenAI returns (OpenAI reports some 401s as `invalid_request_error`, for example). Clients that need precision keep `code`.
- A not-ready refusal now produces one cost event per refused request; event volume grows with the refusals a window causes.
- Unauthenticated callers no longer learn about an injected window from a completion request; `/readyz` stays public and reports it.
- A bucket in deep debt is kept as long as its refill needs; the key count of Redis grows with idle tenants whose refill is slow, bounded by the tenant count.
- A sweep against a hung store can take n × 250 ms instead of 250 ms; refunds are no longer abandoned for the tenants after the slow one.
