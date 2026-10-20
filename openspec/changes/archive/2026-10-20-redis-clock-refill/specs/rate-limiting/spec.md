## ADDED Requirements

### Requirement: Bucket refill uses shared store time

Admission, token adjustment and lease acquisition SHALL compute elapsed refill from the shared store's clock within their atomic operation, independent of gateway replica clocks. A bucket timestamp SHALL NOT move backward. A stored timestamp ahead of store time SHALL preserve the current balance without extra refill until store time catches up. The existing keys and bucket fields SHALL remain compatible.

#### Scenario: Store clock advances

- **WHEN** store time advances by one second for a bucket with refill rate one token per second
- **THEN** exactly one token is restored, up to capacity, regardless of gateway clocks

#### Scenario: Store clock moves backward

- **WHEN** admission, adjustment or lease acquisition observes store time earlier than the saved bucket timestamp
- **THEN** the saved timestamp is preserved and no elapsed-time refill is credited

#### Scenario: Existing timestamp is ahead

- **WHEN** a non-full bucket from an earlier gateway has a timestamp ahead of store time
- **THEN** its balance is preserved, refill resumes only after that timestamp, and retry guidance includes the catch-up delay

#### Scenario: Concurrent replicas share one clock

- **WHEN** 200 requests reach replicas sharing a bucket with capacity 10 and frozen store time
- **THEN** exactly 10 requests are admitted, including when the replicas use different local clocks for lease expiry
