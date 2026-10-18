# Proposal: add-request-validation

## Why

The request subset and the configuration accept values the gateway cannot honor.
- **`max_tokens`:**
  - It has no bounds: near 2⁶³ the token charge overflows to a negative number, the request bypasses the token limit, and the reconciliation refills the tenant's bucket.
  - A value larger than the bucket's capacity is denied with a 429 whose retry time can never come true.
  - Negative values are forwarded to the provider.
- **Fields the gateway does not implement** (`tools`, `tool_choice`, `response_format`, …) are dropped silently with a 200, so a client believes they were applied.
- **Array `content`** gets a misleading "not valid JSON".
- **`metadata.feature_tag`** has no size or character limit and lands verbatim on every event.
- **Configuration:**
  - The lease-size check runs in strict mode too, so a small request burst refuses to start.
  - Nothing checks that the token burst can hold the default reservation, which makes every request without `max_tokens` a 429 forever.
  - `NaN` and `Inf` pass every numeric check.

## What Changes

- **`max_tokens`**, when present, must be a positive integer.
- **Type errors** name the field. Array `content` is reported as unsupported content parts.
- **A request whose input estimate plus reservation exceeds the token burst** is rejected with 400 `invalid_request`, before any charge.
- **Fields that would change the response contract** are rejected with 400 naming the field:
  - top level: `tools`, `tool_choice`, `functions`, `function_call`, `parallel_tool_calls`, `response_format`, `n` other than 1, `logprobs`, `top_logprobs`, `audio`, `modalities` other than `["text"]`, `prediction`, `web_search_options`, `max_completion_tokens`;
  - in messages: `tool_calls`, `tool_call_id`, `function_call`, `audio`.
  - Other fields stay ignored. A field set to `null` or `false` counts as absent.
- **`metadata.feature_tag`** is at most 64 characters of letters, digits, `.`, `_` and `-`, the same bound as `run_id`. Routing-rule tags follow the same character set.
- **Configuration:**
  - The lease-size check applies only in lease mode.
  - With token limiting on, `PUSAQ_TPM_RESERVE_DEFAULT` must not exceed `PUSAQ_TPM_BURST`.
  - Numeric settings reject `NaN` and `Inf`.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `chat-completions`: request validation, unsupported fields, feature-tag bounds.
- `rate-limiting`: a reservation larger than the bucket is a client error; the default reservation must fit the burst.

## Impact

- **Modified:** `internal/gateway/{completions,server}.go`, `internal/config/config.go`, `internal/ratelimit/ratelimit.go`, `docs/gateway.md`, `docs/configuration.md`.
- **Unchanged:** no new dependencies, no new configuration.
