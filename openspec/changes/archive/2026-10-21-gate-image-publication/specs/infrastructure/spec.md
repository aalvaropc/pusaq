## MODIFIED Requirements

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
