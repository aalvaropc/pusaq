## ADDED Requirements

### Requirement: Store health gate per instance

Each gateway instance SHALL track the outcomes of its calls to the limit store — request-limit admission in strict and lease mode, lease refunds, token charge and reconciliation, budget read and spend increment — and SHALL open a store health gate when more than half of its last 20 counted calls, with at least 10 counted, ended without an answer from the store (a deadline, a connection or dial error, a pool timeout). A store reply, including an error reply, SHALL count as answered; a call whose caller canceled it SHALL count as nothing. While the gate is open, every store call SHALL fail at once without contacting the store and SHALL be handled as a store failure: fail-open tenants are admitted with the skipped decision in `fail_open`, fail-closed tenants are refused with 503 `store_unavailable`, and skipped reconciliations, spend increments and lease refunds are logged or counted as lost like timed-out ones. One second after the gate opened, the next store call SHALL start a single probe — a `PING` bounded by 250 ms that no request waits for — and SHALL itself be skipped, as SHALL every call while the probe runs; a probe the store answers SHALL close the gate with an empty window, and any other outcome SHALL reopen it for another second. The outcome of a call admitted before the gate opened SHALL NOT decide the probe. Every transition SHALL be logged as a `breaker_state` record with `component` `store`, the state left and the state entered. The rule, the cool-down and the probe budget are constants stated in the startup log. While the store answers, the gate SHALL add no store call. The gate is per instance; limits during an outage SHALL NOT be described as exact.

#### Scenario: Gate opens on repeated unanswered calls

- **WHEN** 11 of an instance's last 20 store calls end without an answer from the store
- **THEN** the gate opens, one `breaker_state` record with `component` `store` logs `closed` to `open`, and the next store call fails at once without reaching the store

#### Scenario: Few or minority failures keep the gate closed

- **WHEN** fewer than 10 store calls have been counted, all of them unanswered, or 10 of the last 20 were unanswered
- **THEN** the gate stays closed and every call reaches the store

#### Scenario: Replies and cancellations do not open the gate

- **WHEN** store calls end with Redis error replies, or their callers cancel them
- **THEN** the replies count as answered, the cancellations count as nothing, and the gate stays closed

#### Scenario: Open gate admits fail-open tenants at once

- **WHEN** the gate is open and a request arrives from a tenant without `fail_closed`
- **THEN** the request is admitted without contacting the store, its cost event names each skipped decision in `fail_open`, and each is logged as a fail-open warning

#### Scenario: Skipped writes are lost and recorded

- **WHEN** the gate is open when a token reconciliation, a spend increment or a lease refund is due
- **THEN** nothing is sent to the store, the reconciliation and the increment are logged as failed, and the refund counts as a refund failure with its tokens lost

#### Scenario: Lease mode keeps admitting from held tokens

- **WHEN** the gate is open and a replica in lease mode still holds tokens for a tenant
- **THEN** the tenant's requests are admitted from the lease without a store call, and the first request the lease cannot cover fails open or closed at once

#### Scenario: Answered probe closes the gate

- **WHEN** one second has passed since the gate opened, a store call arrives, and the store answers the probe's `PING`
- **THEN** that call is skipped, the gate moves to `half_open` and then `closed`, and the calls after the probe reach the store

#### Scenario: Unanswered probe reopens the gate

- **WHEN** the probe's `PING` ends without an answer within 250 ms
- **THEN** the gate reopens for another second, and every call during the probe and after it is skipped without waiting

#### Scenario: A straggler does not decide the probe

- **WHEN** a call admitted before the gate opened ends while the probe runs
- **THEN** its outcome changes nothing and the probe alone closes or reopens the gate

#### Scenario: Healthy store sees no extra call

- **WHEN** the store answers every call
- **THEN** the gate stays closed, no probe is sent, and the store receives exactly the calls it receives without the gate

## MODIFIED Requirements

### Requirement: Store failure degrades open

If the limiter's backing store is unreachable or errors, the gateway SHALL admit the request (fail-open) and log the degradation — temporarily inexact limits are preferred over a total outage caused by the limiter itself — unless the tenant's policy sets `fail_closed`, in which case the request SHALL be rejected with 503 `store_unavailable` and no provider called. A store call skipped by the instance's open store health gate SHALL be handled as a store failure, without waiting for the store. Rate limiting disabled by configuration SHALL be announced with a startup warning, never silent.

#### Scenario: Redis outage does not reject traffic

- **WHEN** the backing store is down and a request arrives
- **THEN** the request is admitted and the degradation is logged

#### Scenario: Fail-closed tenant is rejected during an outage

- **WHEN** the backing store is down and a request arrives from a tenant whose policy sets `fail_closed`
- **THEN** the response is 503 `store_unavailable`, no provider is called, and the degradation is logged

#### Scenario: Skipped call degrades like a failed one

- **WHEN** the store health gate is open and a request's admission is decided
- **THEN** the request is admitted, or refused for a fail-closed tenant, exactly as after a failed store call, without contacting the store

#### Scenario: Disabled limiting is explicit

- **WHEN** the gateway starts without a configured store address
- **THEN** it serves without rate limiting and logs a warning at startup
