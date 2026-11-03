## Context

The gateway logs JSON lines through `slog`. Each accounted request produces one `cost_event` record (`internal/telemetry` `SlogSink`); the shadow evaluator writes `cache_shadow` records when a neighbor exists; breakers write `breaker_state`; the fallback chain writes `backend breaker open, skipping` and `backend failed, trying next`; limit stores write `... failing open` warnings; startup and shutdown records carry the configured τ, the embedder and drop counters. All of them are already on stdout of the gateway containers, and the load-test verifiers read them with `docker compose logs`.

## Goals / Non-Goals

**Goals:** one command and one Make target that turn those records into six views with no cloud resources, no new dependencies and no change to the event schema; numbers that state their method and their limits.

**Non-Goals:** a live or hosted dashboard, persisted metrics, cloud log collection, and any change to what the gateway emits.

## Decisions

- **Separate command, standard library only.** `cmd/eventreport` with the logic in `internal/report` (parse → aggregate → render). `html/template` escapes every value from the log; the output inlines CSS and SVG and carries no script, so it opens offline and cannot fetch anything.
- **Line parsing.** Each line is read from its first `{`; text before a `|` separator (the `docker compose logs` prefix) names the source container. Lines without JSON, unknown messages and records without the fields their kind requires are counted and skipped. Duplicate `cost_event` request IDs are counted once.
- **Selection and joins.** `-run-id` keeps cost events with that `run_id`. Records keyed by `request_id` (shadow, fallback, fail-open) count only when their request's cost event is selected; `breaker_state` records count when their time falls within the selected events' time span. Records with no matching cost event are reported, not silently dropped.
- **Latency.** Successful requests only, grouped by requested alias and served model; nearest-rank percentiles (`value at rank ⌈p·n/100⌉` of the ascending sample), over the whole-millisecond `latency_ms` the gateway writes. For streams that is the time until the stream ends.
- **Limit kind.** The request limit runs before alias resolution and the token limit after it, so a `rate_limited` event without `requested_model` is a request-limit denial and one with it is a token-limit denial. Fail-open admissions come from the three `failing open` warnings.
- **Shadow cache.** Predicted hit-rate = records labeled `shadow_hit` over eligible successful completions (no `cache_skip`); evaluations without a neighbor, dropped or skipped count as misses. Next to it, the false-positive upper bound = hits whose `neighbor_fingerprint` differs from `response_fingerprint` over hits (identical fingerprints are certainly correct; different ones are candidates). A τ sweep recomputes both from `best_similarity`; the histogram splits similarity by fingerprint agreement. No hit-rate is rendered without its bound.
- **Make target.** `make dashboard` collects `docker compose --profile loadtest logs --no-color` (all services; non-gateway lines are skipped) into `loadtest/out/`, or reads `DASHBOARD_LOG`; `DASHBOARD_RUN_ID` filters. The command fails when the selection holds no cost events, so an empty stack does not produce an empty report.

## Risks / Trade-offs

- Warning messages are matched by text; contract tests feed real gateway, telemetry and semcache output through the parser so a rename fails the suite.
- Local data comes from mock upstreams with synthetic prices; the report states that its figures describe its input only. Dropped telemetry events are absent from every view; shutdown drop counters are shown when present.
- `docker compose logs` re-reads full container logs; long-lived stacks produce large inputs, read in one streaming pass.
