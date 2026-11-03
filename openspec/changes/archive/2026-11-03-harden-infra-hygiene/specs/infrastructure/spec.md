## ADDED Requirements

### Requirement: Infrastructure providers are bounded and locked

Each infrastructure module SHALL constrain every provider to a range within one major version and SHALL commit a dependency lock recording the selected versions and their hashes. Continuous integration SHALL initialize each module from its committed lock without modifying it, and SHALL fail when the configuration needs a provider selection the lock does not record.

#### Scenario: Provider ranges stay within one major version

- **WHEN** a module's provider constraints are read
- **THEN** every provider's allowed range lies within a single major version

#### Scenario: CI does not record provider selections

- **WHEN** continuous integration initializes a module whose configuration needs a provider the committed lock does not record
- **THEN** initialization fails instead of selecting and recording a provider version

## MODIFIED Requirements

### Requirement: A runtime module deploys the gateway for measurement runs only

A runtime module, declared as code with remote state, SHALL deploy the gateway to at least two regions behind a global external Application Load Balancer that follows Cloud Run service health, with one Redis instance per region, a readiness probe on every gateway instance, at least one minimum instance per region, and the mock provider as the upstream on internal ingress. It SHALL be created for a measurement run and destroyed the same day; after destroy nothing it created SHALL remain billing. Every resource SHALL carry the `project=pusaq` label. It SHALL NOT claim to be a standing deployment. The gateway SHALL run as a dedicated service account and SHALL receive its API keys and admin token as Secret Manager secret references, never as plain environment values. That service account SHALL be the only principal the module grants access to those secrets, with read access only. The API keys SHALL NOT be recorded in Terraform state or saved plans; the generated admin token remains in Terraform state, from which the module outputs it to the failover harness.

#### Scenario: Applied for a run

- **WHEN** the runtime module is applied with a published image tag
- **THEN** each region has a gateway service with a readiness probe and a Redis instance, the balancer answers on one global address, and each response names its region

#### Scenario: Destroyed after the run

- **WHEN** the runtime module is destroyed
- **THEN** no Cloud Run service, Redis instance, secret, or forwarding rule of the run remains, and project APIs stay enabled

#### Scenario: Credentials reach the gateway as secret references

- **WHEN** Terraform computes a plan for the runtime module
- **THEN** each gateway reads its API keys and admin token from Secret Manager secret versions, runs as the dedicated service account, and that account holds only read access on those two secrets

#### Scenario: API keys stay out of state

- **WHEN** the runtime module is applied
- **THEN** the API keys are written to Secret Manager and no API key value is recorded in the Terraform state
