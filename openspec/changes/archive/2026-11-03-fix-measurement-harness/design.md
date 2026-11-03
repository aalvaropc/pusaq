## Context

The regional failover measurement combines k6 points, the k6 summary and gateway log records. Local runs read Compose logs; cloud runs read Cloud Logging, which ingests records with a delay. Gateway `readiness_window` records and fail-open warnings carry no run id, and the gateway is unchanged by this change.

## Goals / Non-Goals

Goals: separate stream outcomes as rule 6 requires, measure times from the instant a request was sent, use one stated percentile method, and verify a run only on its own evidence. Non-goals: re-running the cloud measurement, adding a run id to gateway readiness records, or changing any recorded result.

## Decisions

- **Mock latency bound named as a maximum.** Making the setting a true p99 would change the distribution behind every existing recipe (token overrun's 1–2 s delays, local chaos, the runtime module's defaults). Naming it `-latency-max` keeps each recipe's distribution identical and makes its meaning exact. `-latency-p99` remains accepted as the same setting because the runtime module passes it.
- **Stream started = first response byte.** On k6 status 0, `timings.receiving > 0` means a response byte arrived. Checked with k6 1.4.0 against a local server: a response cut after its headers gives status 0 with `receiving` > 0; connection refused, reset before the response and a timeout before the headers give `receiving` = 0. Never-started streams are retried once, like the main arm's transport failures.
- **Send instant recorded by the client.** `Date.now()` before each request becomes the `sent_ms` tag on its point. Points without it fail verification instead of falling back to completion minus duration.
- **Percentiles interpolate at (n − 1)·p**, the method k6 uses for its trend summaries, so chaos latencies compare with the k6 percentiles in the other reports.
- **Run scoping by interval.** Records without a run id are kept when their log time lies between the summary's `started_at` and `ended_at`, widened by `CHAOS_CLOCK_SLACK_SECONDS` (5 s) for client/server clock offset. Exactly one down record for the failed region turns any leftover overlap into a visible failure.
- **Bounded cloud collection.** Collection is complete when Cloud Logging returns at least one cost event per client attempt a gateway answered past readiness, plus the failed region's down record. It polls every `CHAOS_LOG_POLL_SECONDS` (10 s) for at most `CHAOS_LOG_WAIT_SECONDS` (180 s), counting polls rather than reading a clock, then verifies whatever arrived.
- **Shared client module.** Stream classification, the retry rule and duration parsing live in `loadtest/chaos-lib.js`, imported by k6 and exercised with Node.js by `make test-loadtest`.

## Risks / Trade-offs

- Node.js becomes a requirement of `make test-loadtest`; GitHub's Ubuntu runners include it.
- A record logged by an adjacent run inside the slack would be kept; the exactly-one check fails such a run rather than passing it.
- When Cloud Logging drops records, collection waits the full bound before verification reports the gap.
