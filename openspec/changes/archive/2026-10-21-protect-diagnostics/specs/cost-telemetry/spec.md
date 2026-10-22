## ADDED Requirements

### Requirement: Diagnostics exclude request content and raw dependency errors

Gateway diagnostics SHALL exclude provider response bodies, prompt content, credentials, raw transport errors, and recovered panic values. Failure records SHALL use safe categories and correlation metadata. Normalized client errors and cost event fields SHALL remain compatible.

#### Scenario: Sensitive upstream error

- **WHEN** a provider response, transport error, embedding failure, or panic contains a synthetic secret
- **THEN** logs and normalized error responses contain zero occurrences of that secret

#### Scenario: Failure remains observable

- **WHEN** a dependency fails
- **THEN** diagnostics retain a safe failure category and available provider, status, or request ID
