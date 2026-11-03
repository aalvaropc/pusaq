## 1. Terraform

- [x] 1.1 Make the balancer's global address depend on API enablement.
- [x] 1.2 Bound the Google provider to one major version in both modules and align the recorded lock constraints.
- [x] 1.3 Move the gateway's API keys and admin token to Secret Manager behind a dedicated service account, enable the API, and assert the wiring in a mocked-provider runtime test.
- [x] 1.4 State why the readiness probe is tight in the variable descriptions and the operations guide.

## 2. CI and repository

- [x] 2.1 Initialize both modules with a read-only lock in CI and `make infra-validate`, and run both modules' tests.
- [x] 2.2 Ignore Terraform plans, auto-loaded variable files and crash logs; drop the orphan comment.
- [x] 2.3 Write the `make calibrate` curve under the ignored `out/` directory.
- [x] 2.4 Benchmark the content rules on short and 4 KiB English and Spanish prompts.

## 3. Verification

- [x] 3.1 `terraform fmt`, `validate` and `test` for both modules; `make test`, `make lint`, `openspec validate --all`.
