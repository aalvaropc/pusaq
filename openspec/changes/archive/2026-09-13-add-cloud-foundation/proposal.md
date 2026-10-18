# Proposal: add-cloud-foundation

## Why

Pusaq's cloud side has no declared state: no registry of its own, no identity for CI, no way to publish an image without a stored key, and no spending guard scoped to the gateway. Running the gateway in the cloud (Cloud Run, Memorystore) depends on those three things existing first — and they cost nothing to keep.

## What Changes

- Terraform for the cloud foundation in `infra/terraform/foundation/`, state in the existing shared bucket under a Pusaq-specific prefix: an Artifact Registry repository with cleanup policies, a publisher service account bound through workload identity federation to this GitHub repository only (a provider of its own in the existing pool), write access scoped to that one registry, and a monthly budget filtered by the `project=pusaq` label that alerts an existing notification channel. Every resource carries `project=pusaq` through provider default labels.
- Only the APIs the foundation itself needs are managed, and a `destroy` never disables them (they are shared with other workloads in the project).
- A `release-image` workflow publishes the `pusaq` and `mockprovider` images to that registry on every push to `main`, authenticated through workload identity federation with short-lived credentials; concurrent runs are serialized.
- CI gains a Terraform job: formatting check and `validate` with no backend and no credentials.
- `make infra-init|plan|apply|destroy|fmt|validate`; README "Cloud" section; `terraform.tfvars` git-ignored with an example file committed.

## Capabilities

### New Capabilities

- `infrastructure`: the cloud foundation declared as code and the image-publishing contract — what applying it creates, what it never does, and how images reach the registry without long-lived credentials.

### Modified Capabilities

None.

## Impact

- New: `infra/terraform/foundation/*.tf`, `terraform.tfvars.example`, `.terraform.lock.hcl`, `.github/workflows/release-image.yml`.
- Modified: `.github/workflows/ci.yml` (terraform job), `Makefile`, `.gitignore`, README.
- Repository variables `GCP_WORKLOAD_IDENTITY_PROVIDER` and `GCP_SERVICE_ACCOUNT` set once from Terraform outputs. No change to the gateway code.
