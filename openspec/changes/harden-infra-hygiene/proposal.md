## Why

The runtime module passes the gateway's API keys and admin token to Cloud Run as plain environment values and records them in Terraform state. Provider constraints span two major versions, and CI may record provider selections the committed lock does not contain. The balancer's global address is the one resource that does not wait for API enablement. Terraform plans, auto-loaded variable files, crash logs and the calibration curve can land in the working tree unignored, and the CPU cost of the content rules has no benchmark.

## What Changes

- The gateway's API keys and admin token become Secret Manager secrets that Cloud Run reads as secret references. The gateway runs as a dedicated service account that can access only those two secrets. The API keys are written write-only and stay out of state; the admin token stays in state because the module outputs it to the failover harness.
- The runtime module enables the Secret Manager API, and the global address depends on API enablement like the rest of the module.
- Each provider constraint stays within one major version. CI and `make infra-validate` initialize both modules with a read-only dependency lock and run the mocked-provider tests of both modules.
- The readiness probe keeps its timing; variable descriptions and the operations guide state why it is tight.
- `.gitignore` covers Terraform plans, auto-loaded variable files and crash logs; `make calibrate` writes its curve under the ignored `out/` directory.
- A Go benchmark measures the content rules on English and Spanish prompts.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `infrastructure`: runtime credentials reach Cloud Run as secret references readable only by the gateway's identity, API keys stay out of state, and provider versions are bounded and resolved from committed locks.

## Impact

Both Terraform modules, the CI workflow, the Makefile, `.gitignore`, `docs/operations.md`, `docs/gateway.md` and the policy package's tests. Nothing is applied to a cloud project by this change; the runtime module creates its secrets and service account when it is applied for a run.
