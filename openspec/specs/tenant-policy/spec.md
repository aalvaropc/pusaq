# tenant-policy Specification

## Purpose
Per-tenant policies that govern what a tenant may ask for and spend: allowed models, prompt size, a monthly budget in dollars with a cutoff, and how the tenant is treated when a limit's store is unreachable. Enforced after the model is resolved and before anything is charged, so a denial never costs the tenant. It is a policy filter, not a firewall.

## Requirements

### Requirement: Allowed models per tenant

A tenant's policy MAY list the concrete aliases it may be served by. A request whose resolved alias is not listed SHALL be rejected with 403 `policy_denied` before any provider call or token charge, and its cost event SHALL record the denial and the requested and resolved aliases. When `pusaq/auto` resolves to a disallowed alias the request SHALL be denied with a message naming both aliases, never rerouted. A tenant without a policy SHALL be unrestricted.

#### Scenario: Disallowed concrete alias rejected

- **WHEN** a tenant whose policy allows only `pusaq/fast` requests `pusaq/best`
- **THEN** the response is 403 `policy_denied`, no provider is called, no tokens are charged, and the cost event carries the error code and the requested alias

#### Scenario: Router resolution to a disallowed alias is denied, not rerouted

- **WHEN** a tenant whose policy allows only `pusaq/fast` requests `pusaq/auto` with a feature tag whose rule resolves to `pusaq/best`
- **THEN** the response is 403 `policy_denied` naming both aliases, and an untagged `pusaq/auto` request from the same tenant is served by the default alias

#### Scenario: Tenant without a policy is unrestricted

- **WHEN** a tenant with no policy entry requests any alias with any prompt size
- **THEN** the request proceeds as if no policies were configured

### Requirement: Prompt size cap

A tenant's policy MAY cap the prompt size as bytes of message content. A request above the cap SHALL be rejected with 403 `policy_denied` before any provider call or token charge; a request at the cap SHALL be admitted.

#### Scenario: Oversized prompt rejected before any charge

- **WHEN** a tenant with a cap of N bytes sends messages whose content totals more than N bytes
- **THEN** the response is 403 `policy_denied` naming the size and the cap, and nothing is charged

#### Scenario: Prompt at the cap admitted

- **WHEN** the content totals exactly N bytes
- **THEN** the request is admitted

### Requirement: Monthly budget cutoff with bounded overrun

A tenant's policy MAY set a monthly budget in dollars. The gateway SHALL keep the tenant's month-to-date spend in the shared store, keyed by tenant and UTC month of the request's arrival, adding each completion's priced cost atomically once its usage is known — for buffered responses and at stream end alike. Before admitting a budgeted request the gateway SHALL read the month's spend and, if it has reached the budget, reject the request with 402 `budget_exceeded` and a retry time at the start of the next UTC month, calling no provider and charging nothing. The counter is not a reservation: requests in flight when the budget is reached MAY complete, so the overrun is bounded by their cost. Unpriced or failed requests SHALL add nothing.

#### Scenario: Budget exhausted

- **WHEN** a budgeted tenant's month-to-date spend has reached its budget
- **THEN** the response is 402 `budget_exceeded` with a positive `retry_after_ms` and a `Retry-After` header, no provider is called, and the cost event records the error and the spend that caused it

#### Scenario: Priced completion increments the month's spend

- **WHEN** a budgeted tenant's completion succeeds with a priced usage, buffered or streamed
- **THEN** the month's spend grows by that cost once, and the cost event carries the new month-to-date total

#### Scenario: Unpriced or failed request adds nothing

- **WHEN** a budgeted tenant's request fails upstream, or is served by an unpriced model
- **THEN** the month's spend is unchanged

#### Scenario: Budget resets at the UTC month boundary

- **WHEN** a request arrives in a new UTC month
- **THEN** it is checked and counted against that month's counter, which starts at zero

#### Scenario: In-flight requests bound the overrun

- **WHEN** several requests are in flight when the budget is reached
- **THEN** they may complete and be counted, and no request arriving afterwards is admitted until the month resets

### Requirement: Fail-closed store behavior

A tenant's policy MAY set `fail_closed`. For such a tenant, a store failure while deciding the request limit, charging the token limit, or reading the budget SHALL reject the request with 503 `store_unavailable`, calling no provider; tenants without the flag SHALL keep the default fail-open behavior. Token reconciliation is unaffected.

#### Scenario: Request limit store failure rejects

- **WHEN** the store is unreachable when a fail-closed tenant's request is rate-limited
- **THEN** the response is 503 `store_unavailable` and the degradation is logged

#### Scenario: Token charge store failure rejects

- **WHEN** the store is unreachable when a fail-closed tenant's request is charged tokens
- **THEN** the response is 503 `store_unavailable` and no provider is called

#### Scenario: Budget read failure rejects

- **WHEN** the store is unreachable when a fail-closed tenant's budget is read
- **THEN** the response is 503 `store_unavailable`

#### Scenario: Tenants without the flag still fail open

- **WHEN** the store is unreachable for a tenant whose policy does not set `fail_closed`
- **THEN** the request is admitted and the degradation is logged

### Requirement: Policies are validated at startup

Policies SHALL come from configuration and be validated at startup: a tenant not named by any API key (or not the dev tenant when no keys are configured), an unknown field, a duplicate tenant or field, an unknown alias or `pusaq/auto` in the model list, a non-positive prompt cap or budget, or a non-boolean `fail_closed` SHALL prevent startup. A budget SHALL require a price table and a store; `fail_closed` SHALL require a store. Loaded policies SHALL be announced at startup.

#### Scenario: Unknown tenant, field, alias, or value rejected

- **WHEN** the policy configuration names an unknown tenant, field, or alias, lists `pusaq/auto`, or carries a non-positive or malformed value
- **THEN** the gateway fails to start with a configuration error naming the tenant and the problem

#### Scenario: Budget requires a price table and a store

- **WHEN** a tenant policy sets a budget without a price table or without a store address
- **THEN** the gateway fails to start with a configuration error

#### Scenario: Fail-closed requires a store

- **WHEN** a tenant policy sets `fail_closed` without a store address
- **THEN** the gateway fails to start with a configuration error

#### Scenario: Policies are announced at startup

- **WHEN** the gateway starts with tenant policies configured
- **THEN** the startup log states how many tenants have policies and whether budgets are enabled

### Requirement: Content rules with a per-tenant action

The policy filter SHALL classify every completion request's messages with stated heuristics for sensitive data (personal identifiers and secrets, scanned in every message), instruction-override phrasing (scanned outside the system prompt), and time-dependence cues (scanned in user messages), and SHALL record the kinds matched — never the matched text — on the cost event. A tenant's policy MAY turn sensitive-data or instruction-override matches into a 403 `policy_denied` whose message names the kinds; by default matches are only recorded. A content denial SHALL happen after the prompt cap and before any store read, token charge, or provider call. The rules' precision and recall SHALL be measured on a labeled corpus and published with their floors, and the suite SHALL fail below the floors.

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

### Requirement: Cache opt-out per tenant

A tenant's policy MAY keep every request of the tenant out of the semantic cache. Such requests SHALL never be offered to the cache and their cost events SHALL record `tenant_off` as the reason.

#### Scenario: Opted-out tenant

- **WHEN** a tenant whose policy sets `cache=off` sends an otherwise eligible prompt
- **THEN** the shadow cache receives nothing and the cost event records `cache_skip` `tenant_off`
