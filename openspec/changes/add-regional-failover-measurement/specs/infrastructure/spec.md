# infrastructure

## ADDED Requirements

### Requirement: A runtime module deploys the gateway for measurement runs only

A runtime module, declared as code with remote state, SHALL deploy the gateway to at least two regions behind a global external Application Load Balancer that follows Cloud Run service health, with one Redis instance per region, a readiness probe on every gateway instance, at least one minimum instance per region, and the mock provider as the upstream on internal ingress. It SHALL be created for a measurement run and destroyed the same day; after destroy nothing it created SHALL remain billing. Every resource SHALL carry the `project=pusaq` label. It SHALL NOT claim to be a standing deployment.

#### Scenario: Applied for a run

- **WHEN** the runtime module is applied with a published image tag
- **THEN** each region has a gateway service with a readiness probe and a Redis instance, the balancer answers on one global address, and each response names its region

#### Scenario: Destroyed after the run

- **WHEN** the runtime module is destroyed
- **THEN** no Cloud Run service, Redis instance, or forwarding rule of the run remains, and project APIs stay enabled
