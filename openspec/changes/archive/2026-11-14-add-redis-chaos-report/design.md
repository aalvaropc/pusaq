## Context

Every store call on the request path — request-limit admission, token charge, budget read, spend increment and token reconciliation — runs under a 250 ms context deadline, and the Redis client has no internal retries. A failed admission, charge or budget read fails open with a warning and a `fail_open` entry on the cost event, unless the tenant's policy is `fail_closed`, which refuses with 503 `store_unavailable`. A failed spend increment is logged as under-counted. The load-test profile runs two gateway replicas behind nginx, one Redis and a zero-latency mock, all on one host.

## Goals / Non-Goals

Goals: measure what a store outage does to each kind of tenant, under two failure shapes, with every number derived from a stated source and cross-checked. Non-goals: changing gateway behavior, measuring a managed Redis or a network partition, or measuring lease mode.

## Decisions

- **Two arms, two failure shapes.** `docker compose pause` freezes the Redis process while its socket stays open: connections are accepted by the kernel and calls hang until the deadline. `docker compose stop` removes the container from the network: the service name stops resolving, and how long a call takes to fail depends on the resolver and on the Redis client's dial handling, which the run measures. Each arm is its own run id (`<RUN_ID>-hung`, `<RUN_ID>-down`) with recreated gateways and a flushed store, so no state crosses arms.
- **Injection from the host.** The orchestrator starts k6 in the background, injects after the warm-up, restores after the window and records the instants before and after each Docker command with the host clock, the same clock k6 reads inside its container on the same host. A shell trap restores the store whatever stage fails.
- **Periods by send instant.** A request belongs to `before` when sent before the injection command starts, to `during` until the restore command returns, and to `after` from then on.
- **Request-level agreement.** k6 tags each point with the `X-Pusaq-Request-Id` response header. Every answered request must match exactly one cost event of its run with the same tenant and classification, and every event must match a request, so the per-period table is the gateway's view and the client's view at once.
- **Two records per store decision.** The event's `fail_open` entries must equal the `failing open` warnings for that request, and fail-closed refusals must equal the `failing closed` warnings.
- **Overrun over the configured rate.** The fail-open tenant offers twice its request limit, so its bucket is empty when the window opens; the overrun is its admissions sent during the window minus r·W.
- **Lost spend from both sides.** Lost increments are the `spend under-counted` warnings joined to the run's events. After the run the store counter must lie between Σcost − Σlost and Σcost; the difference from the lower end is increments the gateway reported lost that the store applied anyway.
- **Recovery from the client.** Time to recover is the send instant of the last request after the restore that met a store failure, measured from the restore command's return; the first request after it with a store decision is reported too.
- **Event report reads the field.** The cost event's `fail_open` is the documented record of a fail-open decision; warning lines no longer feed the report.

## Risks / Trade-offs

- Docker's pause and stop are proxies: a managed Redis failing over, a partition dropping packets or a slow store behave differently. The report states which shape each arm reproduces.
- One host: Redis, both gateways, nginx and k6 share CPUs, so latencies describe this host only.
- Logs from gateways that do not write `fail_open` show no fail-open admissions in the event report.
