# Tasks: add-cloud-foundation

## 1. Infrastructure code

- [x] 1.1 `infra/terraform/foundation/`: versions (gcs backend, prefix `pusaq/foundation`), provider with default labels, variables, APIs (`disable_on_destroy = false`), Artifact Registry repository with cleanup policies, publisher service account + WIF provider + bindings, label-filtered budget, outputs, `terraform.tfvars.example`; `.terraform.lock.hcl` committed; `terraform fmt` and `validate` clean.
- [x] 1.2 Makefile: `infra-init` (bucket via `-backend-config`), `infra-plan`, `infra-apply`, `infra-destroy`, `infra-fmt`, `infra-validate`; `.gitignore` for `terraform.tfvars`.

## 2. Publishing

- [x] 2.1 `.github/workflows/ci.yml`: terraform job (fmt check, init without backend, validate).
- [x] 2.2 `.github/workflows/release-image.yml`: on push to `main` and manual dispatch; `id-token: write`; WIF auth; registry login with the access token; buildx; push `pusaq` and `mockprovider` tagged `<sha>` and `main`; concurrency group.

## 3. Apply and verify

- [x] 3.1 Operator: application-default credentials for the project; `make infra-init && make infra-plan` — the plan lists API enablements, one registry, one provider, one service account, two IAM members, one budget, nothing else; `make infra-apply`.
- [x] 3.2 Repository variables `GCP_WORKLOAD_IDENTITY_PROVIDER` and `GCP_SERVICE_ACCOUNT` set from `terraform output -raw`.
- [x] 3.3 Verified: the new provider exists with the Pusaq condition and the pre-existing provider is unchanged; the registry lists; no Cloud Run service or Memorystore instance exists for Pusaq.
- [x] 3.4 README "Cloud" section.
- [x] 3.5 CI green including the terraform job; the `release-image` run on `main` succeeds and the registry shows both images with two tags each.
