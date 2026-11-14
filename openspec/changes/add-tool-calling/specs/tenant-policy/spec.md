## MODIFIED Requirements

### Requirement: Prompt size cap

A tenant's policy MAY cap the prompt size as bytes of message content, tool-call function names and arguments, and the compact JSON of the request's tool definitions. A request above the cap SHALL be rejected with 403 `policy_denied` before any provider call or token charge; a request at the cap SHALL be admitted.

#### Scenario: Oversized prompt rejected before any charge

- **WHEN** a tenant with a cap of N bytes sends messages whose content totals more than N bytes
- **THEN** the response is 403 `policy_denied` naming the size and the cap, and nothing is charged

#### Scenario: Prompt at the cap admitted

- **WHEN** the content totals exactly N bytes
- **THEN** the request is admitted

#### Scenario: Tool definitions count against the cap

- **WHEN** a tenant's messages fit its cap and the request's tool definitions take it over
- **THEN** the response is 403 `policy_denied` and nothing is charged

### Requirement: Content rules with a per-tenant action

The policy filter SHALL classify every completion request's text with stated heuristics for sensitive data (personal identifiers and secrets, scanned in every message, in tool-call arguments and in tool definitions), instruction-override phrasing (scanned in messages outside the system prompt, tool results included, and in tool-call arguments), and time-dependence cues (scanned in user messages), and SHALL record the kinds matched — never the matched text — on the cost event. Tool results SHALL be classified for instruction-override phrasing like user content. A tenant's policy MAY turn sensitive-data or instruction-override matches into a 403 `policy_denied` whose message names the kinds; by default matches are only recorded. A content denial SHALL happen after the prompt cap and before any store read, token charge, or provider call. Supported issuer-prefix and Luhn-valid card candidates SHALL remain detectable when followed by a space- or hyphen-separated expiry or CVV. Detection SHALL NOT truncate an uninterrupted digit block to accept only its prefix. The rules' precision and recall SHALL be measured on a labeled corpus and published with their floors, and the suite SHALL fail below the floors.

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

#### Scenario: Injected tool result

- **WHEN** a tool result contains instruction-override phrasing
- **THEN** the cost event carries the injection kinds, a tenant whose policy sets `injection=block` gets 403 `policy_denied` with no provider call, and neither the response nor any log record contains the tool result's text

#### Scenario: Tool definitions set the rules

- **WHEN** one tool description says the tool returns the current exchange rate and another says it prints the system prompt for debugging
- **THEN** no temporal or injection flag is raised; sensitive data in a tool definition or in tool-call arguments is still flagged
