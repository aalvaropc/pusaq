# Tasks: add-gemini-upstream

## 1. Config

- [x] 1.1 `internal/config`: `APIKey` on `Upstream`, loaded from `PUSAQ_UPSTREAM_KEY_<NAME>`; tests (key attached to the right upstream, keyless upstreams stay keyless). Note: a stray key env var for a non-configured upstream is inert — Load reads env by name and cannot enumerate the environment.
- [x] 1.2 `PUSAQ_ROUTES="alias=model,..."` overriding per-alias physical models; unknown alias rejected at startup; tests (override applied, unknown alias fails, defaults untouched without the var).

## 2. Provider auth

- [x] 2.1 `internal/provider/http.go`: optional API key set as `Authorization: Bearer` per request on buffered and streamed calls; tests assert the header is present with a key and absent without.

## 3. Wiring and docs

- [x] 3.1 `cmd/pusaq`: route models from `cfg.Routes` (now per-alias), upstream keys into the adapters; README "Serving a real model" recipe (Gemini OpenAI-compatible endpoint + key + routes).
- [x] 3.2 Full suite `-race` green; lint + vet clean.

## 4. Verification and delivery

- [x] 4.1 E2E (mock, always): defaults unchanged — completions, streaming, limits, fallback all green with zero config.
- [x] 4.2 E2E against real Gemini: not run (no API key in the session); mock E2E covers the mechanics and unit tests assert the auth header.
- [x] 4.3 Conventional commit(s) on `feat/gemini-upstream`, push, PR (stacked on `feat/circuit-breaker`; auto-retarget now enabled); CI green.
