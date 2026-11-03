## Context

The runtime module is the two-region deployment a failover measurement runs against. It generates an admin token with `random_password`, takes throwaway API keys from a variable, and sets both as plain Cloud Run environment values; the gateway runs as the project's default compute service account. `loadtest/chaos-run.sh` reads the admin token from the module's `admin_token` output. CI pins Terraform 1.15.1, and the modules lock `hashicorp/google` 8.7.0 behind the constraint `>= 6.0.0, < 8.7.1`. Dependabot's `compatible-updates` group covers minor and patch updates per module.

## Goals / Non-Goals

**Goals:** credentials out of plain environment values and, where Terraform allows it, out of state; least-privilege access to them; provider ranges within one major version; CI that never records provider selections the repository does not contain; local outputs ignored; a reproducible measurement of the content rules' CPU cost.

**Non-Goals:** changing the readiness probe timing, the failover harness, the calibration tool's flags, or any measurement report. Nothing is applied to a cloud project.

## Decisions

- **Dedicated gateway service account.** The gateway runs as `pusaq-gateway`, which holds `roles/secretmanager.secretAccessor` on the two gateway secrets and no other role. Granting the default compute service account instead would let every workload that runs as it read the secrets. Logs, image pulls and Direct VPC egress use platform identities, not the runtime one.
- **Write-only API keys.** `api_keys` is an ephemeral variable written through `secret_data_wo`, so neither the state nor a saved plan holds the keys. Terraform cannot see a changed write-only value, so `api_keys_version` triggers a new secret version. This needs Terraform 1.11 or newer; the runtime module requires it.
- **Admin token stays in state.** Root module outputs cannot be ephemeral, and the failover harness reads the token from the `admin_token` output. The token therefore remains a regular `random_password` and its secret version uses `secret_data`; the state bucket's readers can read the token. Reading the token from Secret Manager in the harness is out of scope for this change.
- **Pinned secret versions.** Cloud Run references the version numbers the module writes rather than `latest`, so a rotation produces a new revision and the service is created only after the versions and the accessor bindings exist.
- **Provider range `>= 8.7.0, < 9.0.0`.** The lower bound is the locked, tested version. Minor and patch releases satisfy the range, so Dependabot's `compatible-updates` group updates only the lock files; a new major version needs a constraint change and arrives as its own update. The lock files keep 8.7.0 and only their recorded constraint string changes.
- **Read-only lock in CI.** `terraform init -lockfile=readonly` fails when the configuration needs a provider the lock does not record, instead of selecting and recording one in a throwaway checkout. `make infra-validate` uses the same flag and runs both modules' mocked-provider tests, matching CI.
- **Readiness timing documented, not changed.** `/readyz` reads an in-process flag without I/O, so one failed probe means a drain or an injected window, never a slow dependency. The reason lives in the variable descriptions and the operations guide.
- **Calibration output under `out/`.** `make calibrate` creates `out/` and writes `out/calibration.csv`; `/out/` is ignored. The calibration tool's own default path is unchanged.
- **Content-rule benchmark.** `BenchmarkClassify` runs all content rules over short and roughly 4 KiB English and Spanish prompts and reports bytes, time and allocations per operation.

## Risks / Trade-offs

- IAM bindings can take time to propagate; a first revision that fails to read a secret is resolved by applying again.
- The admin token remains readable to anyone who can read the runtime state.
- Each secret version bills while the runtime exists; destroying the runtime deletes the secrets with it.
- The runtime module no longer runs on Terraform older than 1.11.
