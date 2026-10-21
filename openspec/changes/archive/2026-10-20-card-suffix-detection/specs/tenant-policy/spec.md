## MODIFIED Requirements

### Requirement: Content rules with a per-tenant action

The policy filter SHALL classify every completion request's messages with stated heuristics for sensitive data (personal identifiers and secrets, scanned in every message), instruction-override phrasing (scanned outside the system prompt), and time-dependence cues (scanned in user messages), and SHALL record the kinds matched — never the matched text — on the cost event. A tenant's policy MAY turn sensitive-data or instruction-override matches into a 403 `policy_denied` whose message names the kinds; by default matches are only recorded. A content denial SHALL happen after the prompt cap and before any store read, token charge, or provider call. Supported issuer-prefix and Luhn-valid card candidates SHALL remain detectable when followed by a space- or hyphen-separated expiry or CVV. Detection SHALL NOT truncate an uninterrupted digit block to accept only its prefix. The rules' precision and recall SHALL be measured on a labeled corpus and published with their floors, and the suite SHALL fail below the floors.

#### Scenario: Sensitive data blocked for a tenant that asked for it

- **WHEN** a tenant whose policy sets `sensitive=block` sends a prompt containing an email address
- **THEN** the response is 403 `policy_denied` naming `pii:email` and not the address, no provider is called, nothing is charged, and the cost event carries the flag and the denial

#### Scenario: Matches are recorded by default

- **WHEN** a tenant without a content action sends a prompt with instruction-override phrasing
- **THEN** the request is served and its cost event carries the injection kinds

#### Scenario: System prompts set the rules

- **WHEN** an application's system prompt states today's date and a persona, and the user asks a plain question
- **THEN** no temporal or injection flag is raised; sensitive data in a system prompt is still flagged

#### Scenario: Rules are measured

- **WHEN** the content rules run against the labeled corpus
- **THEN** precision and recall per family meet the published floors, and the numbers are published with the rules' stated limits

#### Scenario: Card followed by expiry or CVV

- **WHEN** a message contains a supported checksum-valid card followed by a separated expiry or CVV
- **THEN** pii:card is recorded, a blocking tenant is denied before charges or provider calls, and a logging tenant is served without offering that content to the cache

#### Scenario: Longer uninterrupted number

- **WHEN** an uninterrupted number fails card validation but has a prefix that would validate independently
- **THEN** that prefix alone does not produce a card match
