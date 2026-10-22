## ADDED Requirements

### Requirement: Authentication precedes bounded payload loading

The gateway SHALL authenticate from headers before reading any request body. An authentication rejection SHALL emit exactly one unauthorized cost event with a request ID, no payload-derived run ID, and no provider or limiter call. Authenticated bodies SHALL retain the 1 MiB cap and a positive configurable read deadline, defaulting to 10 seconds. Payload failures SHALL use normalized invalid_request errors and be accounted once. Run IDs SHALL be parsed before request rate limiting.

#### Scenario: Unauthenticated slow body

- **WHEN** missing or invalid credentials accompany an unfinished request body
- **THEN** the gateway sends 401 without reading the body or waiting for it to finish

#### Scenario: Authenticated body stalls

- **WHEN** an authenticated body exceeds its read deadline
- **THEN** the gateway terminates reading and returns a normalized invalid_request response without calling a provider

#### Scenario: Body read completes before a long stream

- **WHEN** a body completes within its deadline and the response stream stays healthy
- **THEN** the body deadline does not truncate the stream

### Requirement: Blocked client writes release request resources

Response writes SHALL use a positive idle deadline, defaulting to 30 seconds, refreshed for each write and flush. A blocked write or failed flush SHALL close the upstream stream and finalize accounting and reservations exactly once. Healthy SSE responses SHALL have no fixed total lifetime imposed by this deadline.

#### Scenario: Slow downstream stops consuming

- **WHEN** a client stops consuming an active response
- **THEN** the write deadline releases the stream and request finalizers exactly once
