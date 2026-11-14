# resilience Specification

## Purpose
How the gateway survives upstream failure: a circuit breaker per backend converts a dead provider from inherited downtime into a fast local decision, and fallback keeps equivalent traffic flowing through the remaining backends.

## Requirements

### Requirement: Circuit breaker per upstream backend

The gateway SHALL track each upstream backend's recent outcomes and open that backend's breaker when the failure rate over a sliding window crosses a configured threshold with at least a minimum number of samples. While open, requests SHALL fail fast for that backend without attempting the call. After a cooldown, the breaker SHALL allow a bounded number of probe requests (half-open): a probe success closes the breaker, a probe failure reopens it. An attempt's outcome SHALL count only while the breaker is still in the state that admitted the attempt: in half-open, only probes admitted since the cooldown elapsed close or reopen the breaker, and the outcome of an attempt admitted before the breaker opened, or in an earlier half-open period, SHALL change nothing; after recovery, the fresh window SHALL hold only attempts admitted since the breaker closed. Releasing an attempt that produced no outcome SHALL free a probe slot only when that attempt is a probe of the current half-open period. The window size, minimum samples, failure threshold, cooldown, and probe count SHALL come from configuration, be validated at startup, and be stated in the startup log. Every state transition SHALL be observable as a log record naming the backend, the previous state, and the new state. Detection time (failure start to breaker open) and recovery time (failure end to breaker closed) SHALL be measurable against a provider that fails at a known instant, by one reproducible command whose results are published with their protocol.

#### Scenario: Breaker opens on sustained failures

- **WHEN** a backend's failure rate over the window crosses the threshold with the minimum sample count met
- **THEN** subsequent requests to that backend are not attempted while the breaker is open

#### Scenario: Insufficient samples never open the breaker

- **WHEN** a backend has fewer results than the minimum sample count, even if all are failures
- **THEN** its breaker stays closed

#### Scenario: Half-open probes are bounded and decisive

- **WHEN** the cooldown elapses and requests arrive
- **THEN** at most the configured number of probes reach the backend; a probe success closes the breaker and a probe failure reopens it

#### Scenario: A straggler does not decide half-open

- **WHEN** an attempt admitted while the breaker was closed ends, successfully or not, after the breaker opened and went half-open with a probe in flight
- **THEN** the breaker stays half-open, admits no extra probe, and the probe's outcome alone closes or reopens it

#### Scenario: A stale release frees no probe slot

- **WHEN** an attempt admitted before the current half-open period is released without an outcome while every probe slot is taken
- **THEN** no further probe is admitted until a current probe ends

#### Scenario: State transitions are observable

- **WHEN** a breaker changes state
- **THEN** the gateway emits one record with the backend name, the state it left, and the state it entered

#### Scenario: Breaker configuration is validated

- **WHEN** the window is not positive, the minimum samples exceed the window, the threshold is outside (0, 1), the cooldown is not positive, or the probe count is not positive
- **THEN** the gateway fails to start with a configuration error

#### Scenario: Detection and recovery measured against a failure window

- **WHEN** the primary backend starts failing at a known instant under steady traffic with a healthy fallback
- **THEN** no client request fails, the breaker's open and closed instants are recorded, and detection and recovery times are computed from those records against the failure window

### Requirement: Fallback to equivalent backends

When a request's backend fails (upstream error) after the retries its failure allows, or its breaker is open, the gateway SHALL try the route's configured fallback backends in order, entering each at most once, preserving the model contract. If every backend fails or is open, the gateway SHALL respond with the normalized `upstream_error`. Each attempt's outcome, retries included, SHALL feed that backend's breaker: an upstream error as a failure, any answer the backend produced as a success. A provider's rejection of the request (400, 413, 422) SHALL end the chain as the client's error. When the client's request ends, the gateway SHALL stop the chain, record no outcome for the interrupted attempt, and release its half-open probe slot.

#### Scenario: Failover serves the request

- **WHEN** the primary backend fails and a fallback backend is healthy
- **THEN** the response comes from the fallback backend and the request succeeds

#### Scenario: Open primary is skipped without an attempt

- **WHEN** the primary's breaker is open
- **THEN** the request goes directly to the next backend, paying no timeout against the dead one

#### Scenario: Everything down is a normalized error

- **WHEN** all backends in the chain fail or are open
- **THEN** the response is 502 `upstream_error` and one cost event records the failed attempts

#### Scenario: A rejected request trips no breaker

- **WHEN** a client repeats a request the primary rejects with 400
- **THEN** every response is 400 `invalid_request`, no fallback backend is called, and every breaker stays closed

#### Scenario: A canceled request feeds no breaker

- **WHEN** the client disconnects during an attempt
- **THEN** no further backend is attempted and no breaker records a failure

#### Scenario: Retries come before fallback

- **WHEN** retries are enabled and the primary keeps failing with 503 while the fallback is healthy
- **THEN** the primary's breaker records one failure per attempt and the fallback serves the request after the primary's retries

### Requirement: Streams fall back only before the first byte

For streaming requests, retries and fallback SHALL apply only to failures that occur before the response stream starts. Once a stream has begun, a mid-stream failure SHALL end the stream with its usual accounting; the gateway SHALL NOT silently restart a stream on the same or another backend. A started stream's outcome SHALL feed its backend's breaker when the stream ends: an upstream failure or a missing `[DONE]` as a failure, a completed stream as a success, and a client disconnect as no outcome.

#### Scenario: Pre-stream failure fails over

- **WHEN** a streaming request's primary backend refuses before any chunk is delivered
- **THEN** the stream is served by the fallback backend

#### Scenario: Pre-stream failure is retried

- **WHEN** retries are enabled and a streaming request's primary answers 503 before the stream opens and then opens it
- **THEN** the stream is served by the primary after one retry

#### Scenario: Mid-stream failure does not restart

- **WHEN** a stream fails after chunks were delivered
- **THEN** the stream ends with the upstream-error accounting and no second backend is tried

#### Scenario: Mid-stream failure is not retried

- **WHEN** retries are enabled and a stream fails after it started
- **THEN** the stream ends with the upstream-error accounting and no second attempt reaches any backend

#### Scenario: Mid-stream failures count against the backend

- **WHEN** streams from one backend keep failing after their first bytes
- **THEN** that backend's breaker records the failures and opens like it would for failures before the first byte

### Requirement: Regional failover under load is measured, not asserted

Regional failover SHALL be measurable by one reproducible command against the runtime deployment: sustained traffic through the balancer while one region's readiness is failed on purpose at a known instant, never by deleting a service. The run SHALL report separately, each from a stated source: the time until new requests are answered by another region, the longest interval without a success, first-attempt failures and how many recovered by one retry, streams complete / interrupted / never started / refused, latency before / during / after per region, and — for a tenant whose quota is split across regions — admissions per region. A stream SHALL count as interrupted only when its response started and ended without `[DONE]`; a stream that fails before its first response byte SHALL count as never started and SHALL be retried once. Times SHALL be measured from the send instant the client records before each request, and latency percentiles SHALL interpolate linearly between the closest ranks. The results SHALL be published with their protocol, including numbers that disappoint, and SHALL never be summarized as "zero dropped streams" unless the count was zero and the failure mode is stated.

#### Scenario: Readiness of one region failed under load

- **WHEN** the run fails region A's readiness for a window while traffic flows through the balancer
- **THEN** the report states the time to the other region, the longest gap, the failure / recovery counts, the stream counts, the per-region latency, and the split tenant's admissions, each cross-checked against the gateways' events

#### Scenario: Control run

- **WHEN** the same load runs with no injection
- **THEN** the report gives the baseline latency per region and shows no failures beyond rate limiting of the split tenant

#### Scenario: Stream fails before its first byte

- **WHEN** a streamed request fails in transport before any response byte arrives
- **THEN** it is reported as never started, not interrupted, and the outcome of its one retry is reported

#### Scenario: Connection set-up precedes the request

- **WHEN** a request spends time connecting before its bytes are written
- **THEN** its time is measured from the instant the client started the request, not from completion minus the request duration

### Requirement: Chaos evidence agrees by tenant and region

The chaos verifier SHALL compare successful main requests including setup and retries, split-tenant admissions and rate-limit rejections, and completed streams against gateway events by region. Local comparisons SHALL require equality after draining; the existing cloud tolerance of five events SHALL apply only to the total main successes. Platform, transport and pre-admission refusals without gateway events SHALL be reported separately rather than treated as missing cost events. Inconsistent comparable evidence SHALL fail the run.

#### Scenario: Local outcomes agree

- **WHEN** a local chaos run finishes and its drained gateway evidence agrees with comparable client outcomes by tenant and region
- **THEN** verification succeeds

#### Scenario: Regional evidence is missing or duplicated

- **WHEN** a split admission, split rejection or completed-stream event differs from the client count in a region
- **THEN** verification fails even if total main successes agree

#### Scenario: Request never enters the pipeline

- **WHEN** a request fails in transport, at the platform or at the injected pre-admission readiness check
- **THEN** the report retains that outcome without requiring a cost event for that request

### Requirement: Chaos evidence is scoped to its run

Gateway records that carry no run id — readiness-window records and fail-open warnings — SHALL count for a chaos run only when logged between the run's start and end on the client clock, widened by a stated clock slack. An injected run SHALL contain exactly one readiness-window down record for the failed region, and a control run none. Cloud collection SHALL poll the log store until it returns a cost event for every client attempt a gateway answered past readiness and, in an injected run, the failed region's down record, for at most a stated wait; it SHALL then verify whatever arrived.

#### Scenario: Record from another run

- **WHEN** the gateway logs hold a readiness-window record or fail-open warning logged outside the run's interval
- **THEN** collection excludes it and verification judges the run on its own records

#### Scenario: Duplicate readiness window

- **WHEN** an injected run's records contain more than one down record for the failed region
- **THEN** verification fails

#### Scenario: Cloud logs arrive with a delay

- **WHEN** Cloud Logging returns the run's records only after several polls
- **THEN** collection waits for them within the stated bound and verification uses them

#### Scenario: Cloud logs stay incomplete

- **WHEN** the bound expires before the expected records arrive
- **THEN** verification runs on the records that arrived and fails on the missing evidence

### Requirement: Same-backend retries are bounded per request

When retries are enabled, an attempt that fails before any response byte reaches the client with a retryable failure SHALL be retried on the same backend before the request falls back. Retryable failures are a transport failure before the upstream answered (connection refused or reset, DNS or TLS failure, a timeout before the response headers) and the statuses 408, 429, 500, 502, 503 and 504. A provider's rejection (400, 413, 422), any other upstream failure, and a client that left SHALL never be retried. A request SHALL make at most the configured number of retries in total across its chain. Before the n-th retry of a backend the gateway SHALL wait a delay drawn uniformly from zero to min(2 s, 200 ms · 2^(n−1)), or the upstream's `Retry-After` when it gives a positive one. A retry SHALL start only if its wait ends before the request's retry deadline — the configured window after the request's first upstream attempt, or the request context's deadline when earlier; a wait that would end after the deadline SHALL send the request to its next backend without waiting. Every retry SHALL ask the backend's breaker for its own admission after the wait and SHALL feed its own outcome; a breaker that refuses the admission SHALL stop retries on that backend. The token charge SHALL be made once per request and reconciled once, whatever the number of retries. Retries SHALL be off by default; the maximum, the budget ratio and the window SHALL come from configuration, be validated at startup, and be stated in the startup log.

#### Scenario: Retryable failure retried on the same backend

- **WHEN** retries are enabled and the primary fails once with a transport failure before answering, or with 408, 429, 500, 502, 503 or 504, and then answers
- **THEN** the response comes from the primary after one retry and the fallback backend is not called

#### Scenario: Rejections and other failures are not retried

- **WHEN** retries are enabled and the primary answers 400, 413 or 422, or answers 401, 404, or 200 without a completion
- **THEN** the primary is attempted once; a rejection ends the request as `invalid_request` and any other failure goes to the fallback backend

#### Scenario: Per-request maximum

- **WHEN** retries are enabled with a maximum of 2 and the primary keeps failing with 500
- **THEN** the primary is attempted three times and the request then goes to the fallback backend

#### Scenario: Retry-After inside the window

- **WHEN** the primary answers 429 with `Retry-After: 1` and the retry window has more than one second left
- **THEN** the gateway waits one second and retries the primary

#### Scenario: Retry-After beyond the window

- **WHEN** the primary answers 503 with a `Retry-After` longer than what remains of the retry window
- **THEN** the gateway does not wait, sends the request to the fallback backend, and the cost event carries `retry_denied` `deadline`

#### Scenario: Open breaker stops retries

- **WHEN** a retry's wait ends with the backend's breaker open
- **THEN** no further attempt reaches that backend and the request goes to the next backend

#### Scenario: Client leaves during the wait

- **WHEN** the client disconnects while a retry waits
- **THEN** no further attempt is made, no breaker records an outcome for it, and the cost event records `client_canceled`

#### Scenario: Retries charge tokens once

- **WHEN** a request with token limiting is retried and then served
- **THEN** its tokens were charged once at admission and reconciled once against the answering attempt's usage

#### Scenario: Retries off by default

- **WHEN** no retry setting is configured
- **THEN** each backend of the chain is attempted at most once per request

#### Scenario: Retry configuration is validated

- **WHEN** the retry maximum is outside 0–5, the budget ratio is not a finite number in (0, 1], or the window is not a positive duration of at most 10 minutes
- **THEN** the gateway fails to start with a configuration error

### Requirement: An instance retry budget bounds retry load

Each gateway instance SHALL keep one retry budget shared by all its requests: a token bucket that each request's first upstream attempt credits with the configured ratio, that refills at one retry per second, that holds at most ten retries, and from which every retry takes one. A retry SHALL be made only when the budget holds one; a retry the budget refuses SHALL send the request to its next backend. A retry taken from the budget and then not made — its backend's breaker refused it or the client left — SHALL be returned. Over any interval of length T in which N requests reached an upstream, an instance SHALL make at most 10 + T · 1/s + ratio · N retries.

#### Scenario: A failing upstream cannot multiply load

- **WHEN** every attempt fails, retries allow two per request, the budget ratio is 0.2, and 1000 requests arrive with no time passing
- **THEN** the instance makes at most 210 retries, against 2000 without the budget

#### Scenario: Budget refusal is visible

- **WHEN** a retryable failure finds the budget empty
- **THEN** the request goes to its next backend without waiting and its cost event carries `retry_denied` `budget`

#### Scenario: Floor refills an idle budget

- **WHEN** the budget is empty and one second passes
- **THEN** one retry is available

### Requirement: Store outage is measured per arm

A limit-store outage SHALL be measurable by one reproducible local command: steady traffic from a fail-open tenant with request and token limits, a budgeted tenant and a fail-closed tenant, while the store is made unavailable on purpose for a fixed window in two separate arms — hung (the store process frozen, calls wait for the limiter budget) and down (the store container stopped) — and then restored. For each arm and each period (before, during and after the window, assigned by the instant the client sent each request), the run SHALL report separately per tenant: requests admitted, limited, refused with `store_unavailable`, other gateway 5xx and transport failures; latency p50/p95/p99 interpolated between the closest ranks; and fail-open admissions by decision from the cost event's `fail_open` field. Per arm it SHALL also report the fail-open tenant's admissions during the window above its configured rate, the budget increments the gateway reported lost with the store counter's value, the store failures during the window that took the whole limiter budget and those that did not, and the time from the restore until the last request that met a store failure. Per replica, from the gateway records collected with the replica that wrote them, it SHALL report when the store health gate first opened — from the injection and from the replica's first logged store failure —, how many times it reopened after a probe, when it last closed relative to the restore, and the time from the restore until the replica's last request that met a store failure. Every answered request SHALL match exactly one cost event of its run with the same tenant and classification; fail-open entries SHALL match the gateway's fail-open warnings; and the store's spend counter SHALL lie between the summed cost minus the reported lost increments and the summed cost. The results SHALL be published with their protocol and SHALL NOT be described as globally exact limits.

#### Scenario: Store hung under load

- **WHEN** the store process is frozen for the window while the three tenants send traffic
- **THEN** the report gives each period's outcomes and latency per tenant, the fail-open admissions by decision, the overrun over the configured rate, the lost and late-applied budget increments and the recovery time, cross-checked against the gateways' events

#### Scenario: Store down under load

- **WHEN** the store container is stopped for the window and started again
- **THEN** the same numbers are reported for that arm, separately from the hung arm

#### Scenario: Gate detection and recovery per replica

- **WHEN** a replica's store health gate opens during the window and closes after the restore
- **THEN** the report gives, for that replica, the gate's opening from the injection and from its first store failure, its reopenings, its closing from the restore, and its last store failure after the restore

#### Scenario: Gate records contradict the window

- **WHEN** a replica's store gate opens before the injection, never opens during a window in which that replica met a store failure, or is still open at the end of the run
- **THEN** verification fails

#### Scenario: Request and event disagree

- **WHEN** an answered request has no cost event, two cost events, or an event with another tenant or classification, or an event has no answered request
- **THEN** verification fails

#### Scenario: Injection without effect

- **WHEN** no request sent during the window met a store failure, or a request sent before it did
- **THEN** verification fails

#### Scenario: Policy violated during the outage

- **WHEN** a fail-closed tenant's event carries `fail_open`, or a fail-open tenant is refused with `store_unavailable`
- **THEN** verification fails

#### Scenario: Spend counter outside its bounds

- **WHEN** the store's spend counter is below the summed cost minus the reported lost increments, or above the summed cost
- **THEN** verification fails
