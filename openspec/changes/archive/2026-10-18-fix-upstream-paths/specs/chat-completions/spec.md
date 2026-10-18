# chat-completions

## ADDED Requirements

### Requirement: Upstream URLs are versioned API base URLs

Each upstream URL SHALL be the base of an OpenAI-compatible API with its version segment included, without a trailing slash. Chat completions, buffered and streamed, SHALL be sent to the base URL followed by `/chat/completions`. A trailing slash in configuration SHALL be ignored.

#### Scenario: Versioned base

- **WHEN** an upstream is configured as `http://host:9090/v1`
- **THEN** its chat calls go to `http://host:9090/v1/chat/completions`

#### Scenario: Provider base with its own version path

- **WHEN** an upstream is configured as `https://host/v1beta/openai/`
- **THEN** its buffered and streamed chat calls go to `https://host/v1beta/openai/chat/completions`
