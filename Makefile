.DEFAULT_GOAL := help

# Load-test parameters (override on the command line: make loadtest LOADTEST_DURATION=300s)
LOADTEST_RPM ?= 600
LOADTEST_BURST ?= 100
LOADTEST_DURATION ?= 120s
LOADTEST_RATE_MULT ?= 10
LOADTEST_TOLL_RPS ?= 8
LOADTEST_TOLL_DURATION ?= 60s
LOADTEST_MODE ?= strict
LOADTEST_LEASE_SIZE ?= 10
# Breaker run parameters (per tenant rate; three tenants share the load)
BREAKER_RPS ?= 8
BREAKER_WARMUP ?= 20s
BREAKER_FAIL_FOR ?= 5s
BREAKER_DURATION ?= 45s
# Token-limit overrun run: token limiting on, emulated upstream latency,
# request limit raised so it never interferes (20 rps per arm).
TPM_LIMIT ?= 6000
TPM_BURST ?= 1000
TPM_RPS ?= 20
TPM_DURATION ?= 60s
TPM_RPM ?= 6000
TPM_RPM_BURST ?= 1000
TPM_MOCK_LATENCY ?= 2s
# Regional failover run (docs/measurements/regional-failover.md): the cloud
# run reads its targets from the runtime module; the local run reuses the
# Compose profile with shorter windows.
CHAOS_EXPERIMENT ?= region-fail
CHAOS_QPS ?= 8
CHAOS_STREAM_QPS ?= 2
CHAOS_SPLIT_QPS ?= 15
CHAOS_WARMUP ?= 60s
CHAOS_FAIL_FOR ?= 60s
CHAOS_DURATION ?= 300s
CHAOS_TIMEOUT ?= 10s
# Profile-wide knobs the other runs leave at their defaults.
LOADTEST_TPM_LIMIT ?= 0
LOADTEST_TPM_BURST ?=
LOADTEST_MOCK_LATENCY ?= 0
LOADTEST_MOCK_CHUNK_DELAY ?= 10ms
LOADTEST_ADMIN_TOKEN ?= local-chaos-token-0123456789
RUN_ID ?= $(shell date -u +%Y%m%dT%H%M%SZ)
RUN_ID := $(RUN_ID)
export LOADTEST_RPM LOADTEST_BURST LOADTEST_DURATION LOADTEST_RATE_MULT LOADTEST_TOLL_RPS LOADTEST_TOLL_DURATION LOADTEST_MODE LOADTEST_LEASE_SIZE \
       BREAKER_RPS BREAKER_WARMUP BREAKER_FAIL_FOR BREAKER_DURATION \
       LOADTEST_TPM_LIMIT LOADTEST_TPM_BURST LOADTEST_MOCK_LATENCY LOADTEST_MOCK_CHUNK_DELAY LOADTEST_ADMIN_TOKEN TPM_RPS TPM_DURATION RUN_ID \
       CHAOS_EXPERIMENT CHAOS_QPS CHAOS_STREAM_QPS CHAOS_SPLIT_QPS CHAOS_WARMUP CHAOS_FAIL_FOR CHAOS_DURATION CHAOS_TIMEOUT

# Cloud foundation (Terraform). The state bucket exists already; its name is
# passed at init time so it is never hardcoded in the configuration.
TF_STATE_BUCKET ?= aalvaropc-tfstate
TF_FOUNDATION := terraform -chdir=infra/terraform/foundation
# Runtime module: the two-region deployment that exists only for a
# measurement run (created before it, destroyed the same day).
TF_RUNTIME := terraform -chdir=infra/terraform/runtime

.PHONY: help up down run mock test test-loadtest lint lint-docs tidy smoke calibrate loadtest loadtest-down breakertest tpmtest chaos-run chaos-local \
        infra-init infra-plan infra-apply infra-destroy infra-fmt infra-validate \
        runtime-init runtime-plan runtime-apply runtime-destroy

help: ## List available targets
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  %-8s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

up: ## Start local environment (Redis + mock provider)
	docker compose up -d --build

down: ## Stop local environment
	docker compose down

run: ## Run the gateway locally
	go run ./cmd/pusaq

mock: ## Run the mock provider locally
	go run ./cmd/mockprovider --latency-p50 200ms --latency-p99 2s

test: ## Run all tests with the race detector
	go test -race -cover ./...

test-loadtest:
	bash loadtest/test.sh

lint: lint-docs ## Run golangci-lint, go vet and lint-docs
	golangci-lint run
	go vet ./...

# Planning and tracking vocabulary the repository does not carry (CLAUDE.md).
DOC_GUARD_RE := \b(roadmaps?|backlogs?|milestones?|follow-?ups?|next (steps?|slice|change|phase)|separate (changes?|capabilit(y|ies))|later|future|phases?|deliverables?|upcoming|eventually|someday|planned|for now|plan b|mechanical gate|known issues?|workarounds?|todo|fixme|tbd|(does|do) not [a-z]+ yet|(not built|nothing to [a-z]+|no [a-z-]+( [a-z-]+){0,3}) yet|yet to|until [a-z -]{0,40}(exists?|arrives?)|earns? (its|their|them)|earned dependency)\b
DOC_GUARD_PATHS := . ':!go.sum' ':!*.lock.hcl' ':!.claude' ':!CLAUDE.md' ':!Makefile' ':!testdata'
DOC_GUARD_FAIL := { echo "lint-docs: planning or tracking language found (see CLAUDE.md)"; exit 1; }

lint-docs: ## Fail on planning or tracking language and unchecked archived tasks
	@git grep -n -I -i -E '$(DOC_GUARD_RE)' -- $(DOC_GUARD_PATHS); s=$$?; [ $$s -eq 1 ] || $(DOC_GUARD_FAIL)
	@git grep -n -I -E '\bV[0-9]\b' -- $(DOC_GUARD_PATHS); s=$$?; [ $$s -eq 1 ] || $(DOC_GUARD_FAIL)
	@git grep -n -E '^\s*- \[ \]' -- openspec/changes/archive; s=$$?; [ $$s -eq 1 ] || $(DOC_GUARD_FAIL)

tidy: ## Tidy module files
	go mod tidy

calibrate: ## Calibrate the cache threshold on the seed dataset (local embedder)
	go run ./cmd/cachecal -pairs testdata/calibration/seed-pairs.csv -out calibration.csv

smoke: ## Send a chat completion through the gateway (gateway + mock must be running)
	curl -sS -X POST localhost:8080/v1/chat/completions \
		-H 'Content-Type: application/json' \
		-d '{"model":"pusaq/fast","messages":[{"role":"user","content":"hello"}]}' \
		-w '\nstatus: %{http_code}\n'

loadtest: ## Limiter load test: 2 replicas behind nginx, k6 in Docker, cross-checked report
	docker compose --profile loadtest up -d --build --force-recreate \
		pusaq-a pusaq-b pusaq-nolimit-a pusaq-nolimit-b mock-lt mock-lt-backup lb
	docker compose exec -T redis redis-cli FLUSHALL
	docker compose exec -T redis redis-cli CONFIG RESETSTAT
	bash loadtest/run-measurement.sh limiter $(RUN_ID)

breakertest: ## Breaker detection/recovery run: primary mock fails on command, k6 keeps traffic flowing
	docker compose --profile loadtest up -d --build --force-recreate \
		pusaq-a pusaq-b pusaq-nolimit-a pusaq-nolimit-b mock-lt mock-lt-backup lb
	docker compose exec -T redis redis-cli FLUSHALL
	bash loadtest/run-measurement.sh breaker $(RUN_ID)

tpmtest: LOADTEST_TPM_LIMIT = $(TPM_LIMIT)
tpmtest: LOADTEST_TPM_BURST = $(TPM_BURST)
tpmtest: LOADTEST_RPM = $(TPM_RPM)
tpmtest: LOADTEST_BURST = $(TPM_RPM_BURST)
tpmtest: LOADTEST_MOCK_LATENCY = $(TPM_MOCK_LATENCY)
tpmtest: ## Token-limit overrun run: under- and over-reserving tenants against an emulated-latency mock
	docker compose --profile loadtest up -d --build --force-recreate \
		pusaq-a pusaq-b pusaq-nolimit-a pusaq-nolimit-b mock-lt mock-lt-backup lb
	docker compose exec -T redis redis-cli FLUSHALL
	bash loadtest/run-measurement.sh tpm $(RUN_ID)

chaos-local: CHAOS_WARMUP = 20s
chaos-local: CHAOS_FAIL_FOR = 20s
chaos-local: CHAOS_DURATION = 90s
chaos-local: LOADTEST_MOCK_LATENCY = 100ms
chaos-local: LOADTEST_MOCK_CHUNK_DELAY = 200ms
chaos-local: ## Failover harness against the Compose profile (no balancer health; checks the plumbing at $0)
	docker compose --profile loadtest up -d --build --force-recreate \
	    pusaq-a pusaq-b pusaq-nolimit-a pusaq-nolimit-b mock-lt mock-lt-backup lb
	docker compose exec -T redis redis-cli FLUSHALL
	bash loadtest/run-measurement.sh chaos $(RUN_ID)

chaos-run: ## Regional failover run against the runtime module (k6 in Docker from here, events from Cloud Logging)
	./loadtest/chaos-run.sh $(RUN_ID)

loadtest-down: ## Stop the load-test profile
	docker compose --profile loadtest down --remove-orphans

infra-init: ## Initialize the cloud foundation (state in the existing bucket)
	$(TF_FOUNDATION) init -backend-config="bucket=$(TF_STATE_BUCKET)"

infra-plan: ## Show what the cloud foundation would change
	$(TF_FOUNDATION) plan

infra-apply: ## Apply the cloud foundation (registry, publisher identity, budget)
	$(TF_FOUNDATION) apply

infra-destroy: ## Destroy the cloud foundation (never disables shared APIs)
	$(TF_FOUNDATION) destroy

infra-fmt: ## Format Terraform files
	terraform fmt -recursive infra/terraform

infra-validate: ## Validate both Terraform modules without touching the backend
	$(TF_FOUNDATION) init -backend=false -input=false >/dev/null && $(TF_FOUNDATION) validate
	$(TF_RUNTIME) init -backend=false -input=false >/dev/null && $(TF_RUNTIME) validate

runtime-init: ## Initialize the runtime module (state in the existing bucket)
	$(TF_RUNTIME) init -backend-config="bucket=$(TF_STATE_BUCKET)"

runtime-plan: ## Show what the runtime module would create
	$(TF_RUNTIME) plan

runtime-apply: ## Create the two-region deployment for a measurement run
	$(TF_RUNTIME) apply

runtime-destroy: ## Destroy the two-region deployment (same day as the run)
	$(TF_RUNTIME) destroy
