.DEFAULT_GOAL := help

.PHONY: help up down run mock test lint tidy smoke

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

smoke: ## Send a smoke request through the pipeline (gateway must be running)
	curl -sS -X POST localhost:8080/v1/echo -d '{"hello":"pusaq"}' -w '\nstatus: %{http_code}\n'
