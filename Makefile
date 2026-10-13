BINARY  := uptime-pulse
IMAGE   ?= ghcr.io/rakaprad/uptime-pulse
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
CONFIG  ?= config.yaml

.DEFAULT_GOAL := help
.PHONY: help run test lint build docker compose-up compose-down clean

help: ## Show available targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "} {printf "  %-14s %s\n", $$1, $$2}'

run: ## Run locally (copies config.example.yaml to config.yaml if missing)
	@test -f $(CONFIG) || cp config.example.yaml $(CONFIG)
	go run ./cmd/uptime-pulse -config $(CONFIG)

test: ## Run unit tests with the race detector and coverage
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

lint: ## Run go vet and golangci-lint
	go vet ./...
	golangci-lint run ./...

build: ## Build a static binary into ./bin
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o bin/$(BINARY) ./cmd/uptime-pulse

docker: ## Build the container image
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .

compose-up: ## Start uptime-pulse + Prometheus + Grafana
	docker compose -f deploy/docker-compose.yml up -d --build

compose-down: ## Stop the compose stack
	docker compose -f deploy/docker-compose.yml down

clean: ## Remove build artifacts
	rm -rf bin coverage.out
