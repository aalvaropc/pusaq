# Design: add-request-validation

## Context

`parseCompletionRequest` (`internal/gateway/completions.go`) decodes into a struct with plain `json.Unmarshal`, so unknown fields vanish and every decode error reads "not valid JSON". `chargeTokens` adds the reservation to the input estimate in `int` and charges whatever results. Config numeric parsing uses `strconv.ParseFloat`, which accepts `NaN` and `Inf`. See proposal.md.

## Goals / Non-Goals

**Goals:**

- No request reaches a provider or the token bucket with a value the gateway cannot honor.
- A client is told which field it got wrong.
- Startup refuses configurations that deny every request or disable a guard silently.

**Non-Goals:**

- Implementing tool calling or response formats; this change rejects them.
- A configurable `max_tokens` cap: the token burst bounds the reservation when token limiting is on, and the provider bounds it otherwise.

## Decisions

1. **Pointer for `max_tokens`.** Like `temperature`, so an explicit value is told apart from an omitted one and an explicit 0 is rejected rather than replaced by the default.

2. **Typed decode first, field check second.** The struct decode reports syntax and type errors (`*json.UnmarshalTypeError` carries the field path); a second decode into raw maps rejects a fixed list of unsupported fields. `DisallowUnknownFields` is not used: harmless fields (`user`, `seed`, `stop`, `top_p`) stay accepted.

3. **Reservation checked against the burst in floating point.** `TokenLimiter` exposes `Limits()` (the real limiter already does); the input estimate plus reservation is compared with the burst as `float64` before the integer charge is computed, which removes the overflow and turns the never-satisfiable 429 into a 400.

4. **One label bound.** `feature_tag` and `run_id` share the 64-character cap; the feature tag also takes the header-safe character set already used for `PUSAQ_REGION`, and the routing rules validate their tags against it so no rule becomes unreachable.

5. **`parseFinite` in config.** One helper parses floats and rejects non-finite values at every numeric setting; `ratelimit.Limits.Validate` rejects them too.

## Risks / Trade-offs

- Clients that send `logprobs`, `n: 2` or tools now get 400 instead of a silent 200. Accepted: the silent 200 misled them.
- A `max_tokens` written in exponent form (`1e3`) is a type error. Accepted: the OpenAI contract is an integer.
