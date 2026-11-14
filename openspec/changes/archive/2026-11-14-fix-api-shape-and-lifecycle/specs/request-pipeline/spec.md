## MODIFIED Requirements

### Requirement: Errors are rendered in one normalized shape

All error responses produced by the gateway — pipeline failures, refusals outside the pipeline, unknown paths and unsupported methods — SHALL share a single JSON error shape `{"error":{"code","message","type","param"}}` with an appropriate HTTP status: a stable machine-readable `code`, a human-readable `message`, an OpenAI-style `type` determined by the code, and `param` naming the offending request field for validation errors and `null` otherwise. Retryable errors SHALL additionally carry `retry_after_ms`. Every error response SHALL carry the request-ID header; an error raised outside the pipeline SHALL carry a fresh ID that no cost event references. A path the gateway does not serve SHALL answer 404 `not_found`; a served path with another method SHALL answer 405 `method_not_allowed` with an `Allow` header listing the path's methods.

#### Scenario: Handler error is normalized

- **WHEN** any pipeline stage or handler fails
- **THEN** the response body is the normalized error JSON and the HTTP status matches the error's category

#### Scenario: Unknown path

- **WHEN** a request targets a path the gateway does not serve
- **THEN** the response is 404 with `Content-Type: application/json`, code `not_found`, type `not_found_error`, `param` null and a request-ID header, and no cost event is emitted

#### Scenario: Unsupported method

- **WHEN** a client sends `GET /v1/chat/completions`
- **THEN** the response is 405 with an `Allow: POST` header and the normalized body with code `method_not_allowed` and a request-ID header

#### Scenario: Validation error names its field

- **WHEN** a chat-completions request omits `model`
- **THEN** the 400 body carries code `invalid_request`, type `invalid_request_error` and param `model`

#### Scenario: Error without a field

- **WHEN** a request is rejected for a reason that names no request field, such as an unknown API key or a body that is not JSON
- **THEN** the body carries `param` set to null and the type of its code
