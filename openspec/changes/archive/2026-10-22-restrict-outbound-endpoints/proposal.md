## Why

Outbound clients must not resend private payloads through redirects or unsafe credential transport.

## What Changes

Validate provider and embedding base URLs, require HTTPS for credentials except explicit loopback development, and disable redirects in all outbound HTTP clients. Keep keyless HTTP mocks and versioned HTTPS bases compatible.

## Capabilities

### Modified Capabilities

- `chat-completions`: Outbound clients must not resend private payloads through redirects or unsafe credential transport.

- `semantic-cache`: Embedding endpoints share URL restrictions and reject redirects.

## Impact

Gateway behavior, configuration, and its specifications.
