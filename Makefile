.DEFAULT_GOAL := help

.PHONY: help up down run mock test lint tidy smoke calibrate

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
