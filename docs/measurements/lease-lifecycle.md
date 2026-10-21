# Measurement: lease ownership during cleanup

## Contract

An admission locks and validates the current tenant entry before acquiring
or consuming tokens. If cleanup retired its captured reference, it retries
with the current entry. Retirement skips a busy tenant, preserves live token,
pending-refund and denial state, and deletes only a matching idle entry.
No tenant wait or Redis call holds the tenant-map mutex. Refund calls remain
outside both mutexes.

Local denials are cached for min(store retry, lease TTL). The initial
response still carries the store's retry guidance; a locally cached denial
carries the remaining local interval. Returned tokens can be used when that
interval expires. This does not change strict mode or Redis scripts.

## Deterministic and concurrent checks, 2026-10-20

The original acquisition and retirement operations were extracted without
changing their behavior. Regression tests then reject stale-entry acquisition,
waiting retirement on a gated tenant, and retirement of a live denial.
The corrected operations pass all lifecycle regressions 100 times with the
race detector, without scheduling sleeps.

| Scenario | Result |
|---|---|
| Captured entry retired before acquisition | Acquisition obtains the current registered entry |
| Tenant a's store call held behind a channel | Retirement returns and tenant b admits before a is released |
| Entry holds tokens, pending refund or an unexpired denial | Entry retained |
| A captured reference has been replaced | Replacement retained |
| 1000 requests and 1000 concurrent sweeps, frozen store time | Admissions + remaining balance = 1000 after drain; lost tokens and refund failures both 0 |
| Store retry 0.5 s, 1 s or 10 s; lease TTL 1 s | Local cache expires after 0.5 s, 1 s or 1 s respectively; returned tokens become usable |
| 200 concurrent requests, frozen store time, capacity 10 | Exactly 10 admissions, including across leased replicas with offset local clocks |

Existing failed-refund accounting tests preserve visible TokensLost.
A busy tenant can delay a sweep; this contract isolates other tenants from
its map-lock wait and does not promise a fixed sweep completion time.

The public-admission integration check also rejects a deliberate isolated
mutation that bypasses ownership validation, detecting lost tokens despite
zero reported refund failures. The mutation is not present in this change.

## Local measurements, 2026-10-20

The Compose profile uses Redis 7, two gateway replicas, k6 and zero-latency
mock providers on one host. Both RPM runs offer 100 requests/s for 120 s
against 600 requests/minute with burst 100, followed by a separate 60 s
latency control; TPM is disabled. Gateway events are drained before the
independent verifier reads them.

### Strict control

Command: `make loadtest LOADTEST_MODE=strict RUN_ID=lifecycle-strict-20261020`.

Admissions **1299** lie inside [1280, 1320]; rejections are 10702. The mock
serves 1299. Gateway a records 651 admissions / 5355 rejections and gateway
b 648 / 5347. There are 12001 events for 12001 requests and zero unexpected
outcomes, dropped iterations, fail-open admissions or co-tenant rejections.
Script calls per limited request are 1.000; p99 toll is 0.08 ms relative to
the unlimited control on this host.

### Lease mode

Command: `make loadtest LOADTEST_MODE=lease RUN_ID=lifecycle-lease-20261020`.

Lease size is 10. Admissions **1299** lie inside [1260, 1320]; rejections
are 10702 and the mock serves 1299. Gateway a records 656 admissions / 5355
rejections and gateway b 643 / 5347. There are 12001 events for 12001
requests. Unexpected outcomes, dropped iterations, fail-open admissions and
co-tenant rejections are zero. Script calls per limited request are 0.321;
p99 toll is 0.01 ms relative to the unlimited control. Two cold NOSCRIPT
responses fall back to EVAL; no admission fails open.

These are local measurements, with no cloud or real provider calls. Small
latency differences on a single host are not deployment latency guarantees.
