# Proposal: harden-config-secrets

## Why

Configuration mistakes leak or misroute secrets:
- **Error messages echo secrets.** A duplicated key or a mistyped separator in `PUSAQ_API_KEYS` puts the whole `key:tenant` pair in the startup error, which the gateway logs; a malformed upstream URL with credentials is echoed the same way.
- **An empty variable disables authentication.** `PUSAQ_API_KEYS` set but empty silently switches to dev mode, which leaves a public service unauthenticated when a secret fails to resolve.
- **Upstream key variables collide.** Upstream names that differ only in punctuation or case (`my-api`, `my_api`, `MY.API`) read the same `PUSAQ_UPSTREAM_KEY_MY_API`, so one credential is sent to several hosts.
- **Fields are not trimmed.** Spaces around fields in `PUSAQ_API_KEYS` are kept: `"sk-a : acme"` stores a key that never matches.

## What Changes

- Configuration errors name the position, the tenant or the upstream, never a key or a URL.
- `PUSAQ_API_KEYS` set but empty fails startup; dev mode needs the variable unset.
- Upstream names whose key variables would collide fail startup.
- Fields in `PUSAQ_API_KEYS` and `PUSAQ_UPSTREAMS` are trimmed.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `tenant-auth`: dev mode only when `PUSAQ_API_KEYS` is unset; configuration errors never echo keys.
- `chat-completions`: upstream key variables are unique per upstream.

## Impact

- Modified: `internal/config/config.go`, `docs/configuration.md`, `docs/gateway.md`.
- No new dependencies, no new configuration.
