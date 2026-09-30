# cost-telemetry

## ADDED Requirements

### Requirement: Events record policy flags and cache eligibility

Every completion event SHALL carry the content-rule kinds the policy filter matched (`policy_flags`, absent when none) and, when the semantic cache could not see the request, the single reason (`cache_skip`). The flags SHALL name kinds only, never content.

#### Scenario: Flagged and served

- **WHEN** a served completion's prompt matched `pii:email`
- **THEN** its event carries `policy_flags` `pii:email` and `cache_skip` `sensitive`

#### Scenario: Eligible request

- **WHEN** a served completion matched nothing and was offered to the cache
- **THEN** its event carries neither field
