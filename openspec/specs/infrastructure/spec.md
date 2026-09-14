# infrastructure Specification

## Purpose

The cloud foundation Pusaq's deployments build on, declared as code: the image registry, the identity CI publishes with, and the spending guard — what applying it creates, what it never does, and how images reach the registry without long-lived credentials.

## Requirements

### Requirement: The cloud foundation is declared as code and runs nothing

The cloud foundation SHALL be declared in version-controlled infrastructure code with remote state, and applying it SHALL create only an image registry with cleanup policies, a publishing identity, and a label-scoped budget. It SHALL NOT create any resource that serves traffic or bills while idle. Every resource it creates SHALL carry the `project=pusaq` label. Destroying it SHALL NOT disable project APIs shared with other workloads.

#### Scenario: Applying creates only registry, identity, and budget

- **WHEN** the foundation is applied to the project
- **THEN** the plan and the resulting state contain the image registry, the publisher identity and its bindings, the budget, and API enablements — and no compute, cache, or load-balancing resource

#### Scenario: Destroying never disables shared APIs

- **WHEN** the foundation is destroyed
- **THEN** the registry, identity, and budget are removed and every project API stays enabled

#### Scenario: Formatting and validity are checked in CI

- **WHEN** a change touches the infrastructure code
- **THEN** continuous integration fails if the code is not canonically formatted or does not validate, without needing cloud credentials

### Requirement: Container images are published per commit without long-lived credentials

Every push to the main branch SHALL publish the gateway and mock-provider images to the registry, tagged with the commit identifier and with the branch name. The publishing workflow SHALL authenticate through workload identity federation, exchanging its short-lived identity token for credentials of a service account whose only permission is writing to that registry; no long-lived cloud credential SHALL be stored in the repository. Tokens from any other repository SHALL be rejected. Concurrent publishing runs SHALL be serialized.

#### Scenario: Push to main publishes both images

- **WHEN** a commit lands on the main branch
- **THEN** the registry holds the gateway and mock-provider images tagged with that commit's identifier and with `main`

#### Scenario: Publishing authenticates through workload identity federation

- **WHEN** the publishing workflow runs
- **THEN** it obtains registry write access from its own identity token, and the repository holds no cloud key or password

#### Scenario: Another repository cannot publish

- **WHEN** a workflow from a different repository presents its identity token to the same provider
- **THEN** the exchange is rejected

#### Scenario: Concurrent pushes are serialized

- **WHEN** two publishing runs are triggered close together
- **THEN** the second waits for the first instead of pushing in parallel
