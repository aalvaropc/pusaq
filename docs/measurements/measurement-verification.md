# Measurement: independent evidence verification

## Protocol

`make test-loadtest` runs deterministic shell fixtures without Docker. It
executes the actual verifier scripts with synthetic k6 summaries, per-attempt
points, gateway events and a stub Docker command. Each case asserts the exit
status. Local measurement recipes execute k6, drain the gateways, collect
evidence when applicable and verify even after a failed stage.

The limiter compares admitted, rejected and unexpected counts for one run
and tenant; admitted counts also match the mock. Chaos compares main
successes (setup and retries included), split admissions and rejections, and
completed streams by region. Local counts match exactly. Cloud main
successes have a shared discrepancy budget of five events across all regions;
other comparisons remain exact. Platform, transport and pre-admission
readiness refusals are reported separately because they have no comparable
cost event.

## Fixture results, 2026-10-20

53 checks pass: consistent sources, unrelated run filtering, missing,
duplicated, reclassified and wrongly located events, disagreement between
points and summary, invalid counters, missing files, cloud main tolerance,
and execution/collection/verification failures. All four Make recipes attempt
verification after a simulated k6 failure. One Make invocation evaluates the
default run id once.

The suite rejects the previous limiter verifier when a gateway event is
removed, and the previous chaos verifier when a setup success is removed.
Both previous implementations exit successfully for that inconsistent
fixture; the regression suite requires failure.

## Local limiter result, 2026-10-20

Command: `make loadtest RUN_ID=verify-strict-20261020`.

| Source | Admitted | Rejected | Unexpected |
|---|---|---|---|
| k6 | 1299 | 10702 | 0 |
| Gateway a | 658 | 5353 | 0 |
| Gateway b | 641 | 5349 | 0 |
| Mock | 1299 | — | — |

Admission bound: [1280, 1320]. Gateway events: 12001 for 12001 requests.
Fail-open admissions: 0. Dropped iterations: 0. Script calls per limited
request: 1.000. The p99 difference from the unlimited control is 0.27 ms
on this host, with Redis in Docker and TPM disabled.

## Local chaos result, 2026-10-20

Command: `make chaos-local RUN_ID=verify-chaos-20261020`.

| Comparable outcome | Region a | Region b | Client/event difference |
|---|---|---|---|
| Split admitted | 362 | 637 | 0 |
| Split rate-limited | 159 | 45 | 0 |
| Completed streams | 116 | 53 | 0 |

The main-success comparison also has zero discrepancy, including setup and
retries. The report separately retains 258 not-ready outcomes without cost
events. Every local comparison uses tolerance 0. This is Compose with nginx
round-robin and deliberate readiness failure; it validates the measurement
plumbing and does not measure a cloud load balancer's failover time.
