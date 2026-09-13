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
RUN_ID ?= $(shell date -u +%Y%m%dT%H%M%SZ)
export LOADTEST_RPM LOADTEST_BURST LOADTEST_DURATION LOADTEST_RATE_MULT LOADTEST_TOLL_RPS LOADTEST_TOLL_DURATION LOADTEST_MODE LOADTEST_LEASE_SIZE RUN_ID

# Cloud foundation (Terraform). The state bucket exists already; its name is
# passed at init time so it is never hardcoded in the configuration.
TF_STATE_BUCKET ?= aalvaropc-tfstate
TF_FOUNDATION := terraform -chdir=infra/terraform/foundation

.PHONY: help up down run mock test lint tidy smoke calibrate loadtest loadtest-down \
        infra-init infra-plan infra-apply infra-destroy infra-fmt infra-validate

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

lint: ## Run golangci-lint and go vet
	golangci-lint run
	go vet ./...

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
		pusaq-a pusaq-b pusaq-nolimit-a pusaq-nolimit-b mock-lt lb
	docker compose exec -T redis redis-cli FLUSHALL
	docker compose exec -T redis redis-cli CONFIG RESETSTAT
	docker compose --profile loadtest run --rm --user "$$(id -u):$$(id -g)" k6
	./loadtest/verify.sh $(RUN_ID)

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

infra-validate: ## Validate Terraform without touching the backend
	$(TF_FOUNDATION) init -backend=false -input=false >/dev/null && $(TF_FOUNDATION) validate
