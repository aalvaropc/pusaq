# cost-telemetry

## ADDED Requirements

### Requirement: Events record monthly spend for budgeted tenants

For tenants with a monthly budget, the cost event SHALL carry the tenant's month-to-date spend in dollars: the total after this request was counted, or the total that caused a budget denial. Unbudgeted tenants' events SHALL carry no spend field.

#### Scenario: Spend after a priced completion

- **WHEN** a budgeted tenant's priced completion is counted
- **THEN** its cost event carries the month's total including that request

#### Scenario: Spend on a budget denial

- **WHEN** a budgeted tenant's request is rejected because the budget is exhausted
- **THEN** its cost event carries the spend total that caused the denial
