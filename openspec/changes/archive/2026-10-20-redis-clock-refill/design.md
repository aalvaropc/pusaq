## Context

Compose uses Redis 7 and the runtime module declares Redis 7.2. Effects replication permits TIME inside a writing Lua script. The local lease still needs a clock for its own expiration.

## Goals / Non-Goals

All three bucket operations share one refill calculation. Changing quota policy, lease concurrency or TPM refunds is out of scope.

## Decisions

Read TIME in Lua and convert seconds/microseconds to integer milliseconds. Clamp the effective timestamp to the stored timestamp and persist it with the balance. Denial retry times include the delay until the saved timestamp is reached. Reuse small Lua prefixes/suffixes for refill and persistence, keeping one network round-trip and one atomic mutation.

Remove the gateway-time argument and the strict limiter's test clock. Miniredis SetTime drives refill tests; the lease fixture advances its local clock and store time together, except in explicit clock-offset tests.

## Risks / Trade-offs

Store wall-clock jumps remain an infrastructure property. A timestamp ahead of store time causes conservative under-admission instead of balance resets. Deploy all local replicas together; mixed versions retain the previous writer's timing behavior. Keys and fields require no migration or flushing.
