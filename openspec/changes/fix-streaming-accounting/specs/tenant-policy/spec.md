# tenant-policy

## MODIFIED Requirements

### Requirement: Monthly budget cutoff with bounded overrun

A tenant's policy MAY set a monthly budget in dollars. The gateway SHALL keep the tenant's month-to-date spend in the shared store, keyed by tenant and UTC month of the request's arrival, adding each completion's priced cost — measured, or estimated when a provider answered without usage — atomically when the request ends, for buffered responses and at stream end alike. Before admitting a budgeted request the gateway SHALL read the month's spend and, if it has reached the budget, reject the request with 402 `budget_exceeded` and a retry time at the start of the next UTC month, calling no provider and charging nothing. The counter is not a reservation: requests in flight when the budget is reached MAY complete, so the overrun is bounded by their cost. Unpriced requests and requests no provider answered SHALL add nothing.

#### Scenario: Budget exhausted

- **WHEN** a budgeted tenant's month-to-date spend has reached its budget
- **THEN** the response is 402 `budget_exceeded` with a positive `retry_after_ms` and a `Retry-After` header, no provider is called, and the cost event records the error and the spend that caused it

#### Scenario: Priced completion increments the month's spend

- **WHEN** a budgeted tenant's completion succeeds with a priced usage, buffered or streamed
- **THEN** the month's spend grows by that cost once, and the cost event carries the new month-to-date total

#### Scenario: Stream without usage counts its estimate

- **WHEN** a budgeted tenant's stream ends without a usage chunk
- **THEN** the month's spend grows by the estimated cost of its reservation

#### Scenario: Unpriced or failed request adds nothing

- **WHEN** a budgeted tenant's request fails upstream before any answer, or is served by an unpriced model
- **THEN** the month's spend is unchanged

#### Scenario: Budget resets at the UTC month boundary

- **WHEN** a request arrives in a new UTC month
- **THEN** it is checked and counted against that month's counter, which starts at zero

#### Scenario: In-flight requests bound the overrun

- **WHEN** several requests are in flight when the budget is reached
- **THEN** they may complete and be counted, and no request arriving afterwards is admitted until the month resets
