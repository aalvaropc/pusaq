# Design: harden-config-secrets

## Context

`parseAPIKeys` and `parseUpstreams` (`internal/config/config.go`) quote the offending pair in their errors; `main` logs any startup error. `Load` reads `PUSAQ_API_KEYS` with `ok && v != ""`, so an empty value behaves like an unset one. `envKeyName` maps an upstream name to its key variable by upper-casing it and replacing every other character with `_`. See proposal.md.

## Goals / Non-Goals

**Goals:**

- No startup error contains an API key or a URL.
- Authentication is off only by an explicit absence of keys.
- One key variable belongs to one upstream.

**Non-Goals:**

- Hashing keys at rest or reading them from a secret manager.

## Decisions

1. **Errors by position and name.** Malformed pairs are reported by their position (`pair 2 is malformed`); a duplicate key names the two tenants that share it; an unparsable upstream URL names the upstream only.

2. **Unset versus empty.** `Load` already receives a lookup that tells them apart; an empty `PUSAQ_API_KEYS` goes to the parser, which reports that no pairs were found.

3. **Collision check in `parseUpstreams`.** A map from `envKeyName(name)` to the first name that produced it rejects the second.

4. **Trim each field.** The same rule tenant policies already follow.

## Risks / Trade-offs

- An environment that sets `PUSAQ_API_KEYS=""` on purpose to get dev mode now fails to start. Accepted: unsetting the variable is explicit, and the docs already describe it that way.
