## ADDED Requirements

### Requirement: Upstream attempts carry an idempotency key

Every upstream attempt of a completion, buffered or streamed, SHALL carry an `Idempotency-Key` header of the form `pusaq-<request id>`, built from the request ID the gateway generated and returned in `X-Pusaq-Request-Id`: the same on every retry and every fallback attempt of one request, and different for every request. The key SHALL NOT appear in the upstream request body. A client-sent `Idempotency-Key` SHALL neither be forwarded nor folded into the key, so no client can choose the key another request or tenant reaches the upstream with. The gateway SHALL NOT claim that an upstream deduplicates attempts: an upstream that ignores the header treats each attempt as a new request.

#### Scenario: One key across retries and fallback

- **WHEN** a request is retried on the primary and then served by the fallback backend
- **THEN** every attempt carries the same `Idempotency-Key`, `pusaq-` followed by the request ID the response reports

#### Scenario: Distinct requests carry distinct keys

- **WHEN** a client sends the same completion twice
- **THEN** the upstream receives two different keys

#### Scenario: A client key is not forwarded

- **WHEN** a client sends `Idempotency-Key: k1` with a completion
- **THEN** the upstream receives the gateway's key and never `k1`

#### Scenario: Streamed attempts carry the key

- **WHEN** a streamed completion is opened upstream
- **THEN** the upstream request carries the request's key in its headers and not in its body
