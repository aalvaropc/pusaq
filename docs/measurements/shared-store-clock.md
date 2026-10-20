# Measurement: refill from the shared store clock

## Contract

Admission, adjustment and lease acquisition read Redis time inside their
atomic Lua operation. They preserve the maximum saved timestamp, so a clock
rollback does not credit elapsed time twice. Denial retry guidance includes
the catch-up delay. The keys `rl:{tenant}:dimension` and fields `tokens` and
`ts` retain their representation; balances are not reset on deployment.
Redis 7 uses effects replication and permits TIME inside writing scripts
([Redis scripting documentation](https://redis.io/docs/latest/develop/programmability/eval-intro/)).

Store wall-clock jumps remain an infrastructure property. An existing
bucket timestamp ahead of Redis time conservatively delays refill. Deploy
the same implementation to replicas sharing a bucket: older writers still
supply their own timestamp. Regional stores still enforce regional quotas;
this does not create global quota coordination.

## Deterministic verification, 2026-10-20

`go test -count=1 -race ./internal/ratelimit` passes with miniredis controlling
store time and the actual Lua scripts executing.

| Scenario | Result |
|---|---|
| Store time advances 1 s at 1 token/s | Exactly one token becomes available |
| Admission, adjustment and acquisition observe a 1 s rollback three times | Timestamp preserved; no extra refill |
| A denial during that rollback needs one token | Retry is 2 s, including catch-up |
| An existing empty bucket is 60 s ahead at 1 token/s | Balance preserved, retry 61 s; one token after catch-up plus 1 s |
| 200 concurrent strict requests, frozen store time, capacity 10 | Exactly 10 admitted |
| 200 concurrent requests through two lease replicas with local clocks 48 h apart, frozen store time, capacity 10 | Exactly 10 admitted |

All four new regression functions reject the original replica-time
implementation. Two deliberate mutations are rejected independently:
removing the timestamp clamp and omitting the catch-up delay from retries.
No host or Redis operating-system clock is changed by these tests.

## Local Redis measurements, 2026-10-20

All runs use the Compose load-test profile, Redis 7, two gateway replicas,
k6 and mock providers on one host. Each command drains gateway events
before independent verification. There are no cloud resources or real
provider calls in these runs.

### Strict RPM

Command: `make loadtest RUN_ID=clock-strict-20261020`.

Strict mode admits **1299**, inside [1280, 1320], and rejects 10702.
The mock serves 1299. Gateway a records 654 admissions and 5352 rejections;
gateway b records 645 admissions and 5350 rejections. There are 12001 cost
events for 12001 requests, zero unexpected outcomes, zero dropped iterations
and zero fail-open admissions. Script calls per limited request remain
1.000. The p99 toll is 0.34 ms relative to the unlimited control on this
host, with Redis in Docker and TPM disabled.

### Leased RPM

Command: `make loadtest LOADTEST_MODE=lease RUN_ID=clock-lease-20261020`.

Both RPM runs use 600 requests/minute, burst 100, 100 offered requests/s
for 120 s, and a separate 60 s latency control. Lease size is 10. Lease
mode admits **1299**, inside [1260, 1320], and rejects 10702. The mock
serves 1299; gateway a records 664 admissions and 5343 rejections, and
gateway b records 635 admissions and 5359 rejections. There are 12001 cost
events for 12001 requests, zero unexpected outcomes, zero dropped iterations
and zero fail-open admissions. Script calls per limited request are 0.320.
The p99 toll is -0.14 ms relative to the unlimited control; this small
negative difference is measurement noise, not evidence of a speedup.

### TPM reconciliation

Command: `make tpmtest RUN_ID=clock-tpm-20261020`.

At 6000 tokens/minute, burst 1000, 20 requests/s per arm for 60 s, the
mock takes 2 s and consumes 23 tokens per completion. The under-reserving
arm charges 10 tokens initially; the over-reserving arm charges 66.

| Arm | Admitted (k6, gateway and mock agree) | Rejected | Actual tokens | Peak overrun | End overrun |
|---|---|---|---|---|---|
| Under | 306 | 895 | 7038 | 463.12 | 0 |
| Over | 289 | 912 | 6647 | 0 | 0 |

The measured peak stays within the verifier's bound from reservation error
and concurrent in-flight requests. Reconciliation failures, fail-open
admissions, unexpected outcomes and dropped iterations are all zero.

### Breaker and fallback

Command: `make breakertest RUN_ID=clock-breaker-20261020`.

Three tenants each send 8 requests/s for 45 s. The primary mock fails for
5 s after 20 s of warm-up; mocks have zero latency. All **1081** client
requests return 200, with zero 502s, unexpected outcomes or dropped
iterations. Gateway events total 540 on a and 541 on b.

| Replica | Detection | Open duration | Recovery after failure ends | Probes | Extra attempts | Backup completions |
|---|---|---|---|---|---|---|
| a | 0.875 s | 10.085 s | 5.960 s | 1 | 11 | 131 |
| b | 0.834 s | 10.083 s | 5.917 s | 1 | 11 | 131 |

Both breakers close after one probe without reopening. No upstream error
reaches a client. These timings describe the local mock experiment.

### Local chaos

Command: `make chaos-local RUN_ID=clock-chaos-20261020`.

The run lasts 90 s with a 20 s readiness failure after 20 s of warm-up,
8 main requests/s, 2 streams/s and 15 split-quota requests/s. Mock latency
is 100 ms with a 200 ms stream chunk delay. The local nginx balancer uses
round-robin and does not remove an unready region.

| Comparable outcome | local-a (client = gateway) | local-b (client = gateway) | Absolute difference |
|---|---|---|---|
| Main successes, including setup and retries | 259 | 430 | 0 |
| Split admissions | 419 | 580 | 0 |
| Split rejections | 98 | 99 | 0 |
| Completed streams | 112 | 57 | 0 |

Every regional comparison uses zero tolerance. The main workload records
646 first-attempt successes, 75 first-attempt failures, 43 successful retries
and 32 failed retries. Streams complete 169 times; 11 are finally refused
and none are interrupted. The verifier reports 261 `not_ready` client
outcomes without comparable cost events separately. There are zero
unexpected outcomes, dropped iterations or fail-open warnings.

The first main request sent after the flip and answered by the other region
is at 0.252 s; the longest interval between successful sends is 0.501 s.
These are local harness observations, not cloud failover guarantees. They
do not exercise a hard regional outage or loss of an in-flight stream.

## Repository checks

All Go packages pass `make test` with the race detector (256 test functions
in the repository), and all 53 measurement fixture checks pass.
`make lint`, `shellcheck loadtest/*.sh`, `go build ./...`, documentation
lint and strict OpenSpec validation pass locally.
