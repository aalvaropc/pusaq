## MODIFIED Requirements

### Requirement: Events record the serving backend and attempts

Every cost event SHALL record how many backend attempts were made, zero when none, how many of those attempts were same-backend retries (`retries`), zero when none, and, when a backend answered, which upstream backend served the request, so failover and retries are observable, each on its own, from the event stream alone — including for requests that exhausted the fallback chain. Attempts minus retries SHALL be the number of backends attempted. When a retryable failure was not retried because the instance retry budget was empty or the retry deadline left no room, the event SHALL name each such reason in `retry_denied` (`budget`, `deadline`); an event without `retry_denied` SHALL mean no retry was refused for either reason.

#### Scenario: Failover visible in the event

- **WHEN** a request is served by a fallback backend after the primary failed
- **THEN** its cost event names the fallback backend and an attempt count greater than one

#### Scenario: Exhausted chain records its attempts

- **WHEN** retries are off and every backend of a two-backend chain fails
- **THEN** its cost event carries `attempts` 2 and no served backend

#### Scenario: A retry is told apart from a fallback

- **WHEN** the primary fails once with 503, is retried, and answers
- **THEN** the cost event carries `attempts` 2, `retries` 1, and the primary as the serving backend

#### Scenario: Retries then fallback

- **WHEN** retries allow one per request, the primary fails twice, and the fallback answers
- **THEN** the cost event carries `attempts` 3, `retries` 1, and the fallback as the serving backend

#### Scenario: Budget refusal recorded

- **WHEN** a request's retryable failure is not retried because the retry budget is empty
- **THEN** its cost event carries `retry_denied` `budget`

#### Scenario: No retry machinery, no denial

- **WHEN** retries are off
- **THEN** every cost event carries `retries` 0 and no `retry_denied`
