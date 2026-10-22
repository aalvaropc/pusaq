# infrastructure Specification

## Purpose

Pusaq's cloud side, declared as code: the foundation deployments build on (the image registry, the identity CI publishes with, and the spending guard — what applying it creates, what it never does, and how images reach the registry without long-lived credentials), and the runtime module that exists only for a measurement run and is destroyed after it.

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

With cloud publishing configured, a push to main SHALL publish both images only after all mandatory checks succeed for the exact commit. Images SHALL carry immutable commit tags. PRs, other branches, failed or cancelled verification, and absent cloud configuration SHALL NOT authenticate or publish. Only the publishing job SHALL request an identity token. Federation SHALL reject identities outside the configured numeric repository and owner IDs, main ref, push event, and approved calling and reusable workflow paths. The publisher SHALL have write access only to the image registry, with no stored long-lived credential. Publishing runs SHALL be serialized.

#### Scenario: Push to main publishes both images

- **WHEN** a push to main passes every mandatory check and cloud variables are configured
- **THEN** both images are published under the verified commit SHA

#### Scenario: Unverified execution does not authenticate

- **WHEN** verification fails or is cancelled, the event is a PR, the ref is not main, or cloud variables are absent
- **THEN** no cloud authentication or image publication occurs

#### Scenario: Another repository cannot publish

- **WHEN** any required repository, owner, ref, event, or workflow claim differs
- **THEN** federation rejects the identity

#### Scenario: Publishing authenticates through workload identity federation

- **WHEN** verification runs
- **THEN** only the publishing job can request an identity token and the publisher can write only to its registry

#### Scenario: Concurrent pushes are serialized

- **WHEN** two verified pushes request publication
- **THEN** publications do not run in parallel

### Requirement: A runtime module deploys the gateway for measurement runs only

A runtime module, declared as code with remote state, SHALL deploy the gateway to at least two regions behind a global external Application Load Balancer that follows Cloud Run service health, with one Redis instance per region, a readiness probe on every gateway instance, at least one minimum instance per region, and the mock provider as the upstream on internal ingress. It SHALL be created for a measurement run and destroyed the same day; after destroy nothing it created SHALL remain billing. Every resource SHALL carry the `project=pusaq` label. It SHALL NOT claim to be a standing deployment.

#### Scenario: Applied for a run

- **WHEN** the runtime module is applied with a published image tag
- **THEN** each region has a gateway service with a readiness probe and a Redis instance, the balancer answers on one global address, and each response names its region

#### Scenario: Destroyed after the run

- **WHEN** the runtime module is destroyed
- **THEN** no Cloud Run service, Redis instance, or forwarding rule of the run remains, and project APIs stay enabled
