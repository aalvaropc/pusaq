## Context

See proposal.md. Admission captures a map entry before locking the tenant; idle cleanup can delete it in that gap. Retirement also locks tenants while holding the map mutex.

## Goals / Non-Goals

Preserve token ownership and cross-tenant admission while keeping the existing lease algorithm, Redis operations and loss accounting. General gateway shutdown and admission after Close are out of scope.

## Decisions

After locking a captured entry, briefly check its map identity; retry lookup if it was retired. Extract acquisition and retirement operations so stale references and busy entries can be tested directly without scheduling sleeps.

Retirement first tries the tenant mutex outside the map lock. Busy entries remain for another sweep. Recheck tokens, pending refunds, denial expiry and map identity before deletion. Redis calls never hold the map mutex, and refunds remain outside both mutexes. Reference counters and new background workers are unnecessary.

Keep denial caching capped by min(store retry, lease TTL), preserving current retry response behavior. A bounded cache interval lets returned tokens become available before a longer original retry.

## Risks / Trade-offs

Busy entries may survive a sweep; repeated cleanup reclaims them after they become idle. Waiting for one tenant can delay that sweep but cannot hold the tenant map hostage. Refund failures remain visible in TokensLost. Frozen-clock ledger tests isolate ownership from refill. No data migration is required.
