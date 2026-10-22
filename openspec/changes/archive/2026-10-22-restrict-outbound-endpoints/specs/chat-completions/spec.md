## ADDED Requirements

### Requirement: Outbound provider endpoints restrict credential transport

Configured provider base URLs SHALL be absolute HTTP or HTTPS URLs without user information, query strings, fragments, or invalid ports. Endpoints carrying credentials SHALL use HTTPS, except explicit development mode with localhost or a literal loopback IP. Keyless HTTP mocks SHALL remain supported. Provider clients SHALL reject redirects without forwarding any body or credentials to the redirect destination. Configuration errors SHALL not echo endpoint URLs or credentials.

#### Scenario: Redirect does not resend private data

- **WHEN** a buffered or streaming provider returns a 307 or 308 redirect
- **THEN** the destination receives zero requests and the original attempt becomes a normalized upstream error

#### Scenario: Invalid or plaintext keyed endpoint

- **WHEN** an endpoint violates URL restrictions or uses credentials over HTTP outside the explicit loopback development exception
- **THEN** startup fails without echoing its URL or key

#### Scenario: Compatible versioned endpoint

- **WHEN** a valid HTTPS base URL contains its API version path
- **THEN** requests preserve the configured path and carry credentials only to that endpoint
