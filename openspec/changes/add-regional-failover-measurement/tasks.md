# Tasks: add-regional-failover-measurement

## 1. Gateway and mock

- [x] 1.1 `not_ready` error code; `region` and `run_id` on the cost event and in the log sink; `metadata.run_id` parsed and validated; `PUSAQ_REGION` and `PUSAQ_ADMIN_TOKEN`.
- [x] 1.2 `X-Pusaq-Region` on every response; injected not-ready window (`POST /admin/readiness`, token, `readiness_window` log) that refuses new requests and leaves in-flight streams alone; drain unchanged; tests.
- [x] 1.3 Mock `--stream-chunk-delay`; test.

## 2. Runtime module and harness

- [x] 2.1 `infra/terraform/runtime/`: APIs, VPC and subnets, Memorystore per region, mock and gateway services with readiness probe and Direct VPC egress, serverless NEGs, backend service (optional outlier detection), URL map, proxy, address, forwarding rule; outputs; `make runtime-*`; CI validates both modules.
- [x] 2.2 `loadtest/chaos.js` (main, stream, split, probe, flip), `chaos-run.sh`, `fetch-events.sh` (Cloud Logging or Compose logs), `verify-chaos.sh`; Compose profile knobs (region labels, admin token, chunk delay); `make chaos-run`, `make chaos-local`.
- [x] 2.3 `make chaos-local` green; suite `-race`, lint, shellcheck; conventional commits; draft PR (harness and runtime).

## 3. Measurement and delivery

- [ ] 3.1 Operator: foundation applied, repository variables set, images published for the commit; runtime `terraform.tfvars` with the image SHA; `make runtime-init && make runtime-plan && make runtime-apply`.
- [ ] 3.2 Read-only smoke against the balancer and each region; `make chaos-run CHAOS_EXPERIMENT=none` (control), `make chaos-run` (region-fail), optionally a run with `outlier_detection = true`.
- [ ] 3.3 `docs/measurements/regional-failover.md` with the numbers; README Measurements bullet; `docs/design.md` §8 row; draft PR (measurement).
- [ ] 3.4 Operator: `make runtime-destroy` the same day; confirm nothing is left (`gcloud run services list`, `gcloud redis instances list`, `gcloud compute forwarding-rules list`).
