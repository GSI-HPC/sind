# SPDX-License-Identifier: LGPL-3.0-or-later

BINARY    := sind
CMD       := ./cmd/sind
VERSION   ?= dev
LDFLAGS   := -X main.version=$(VERSION)

.PHONY: build lint test test-integration coverage image clean help

build: ## Build the sind binary
	CGO_ENABLED=0 go build -trimpath -ldflags='$(LDFLAGS)' -o $(BINARY) $(CMD)

lint: ## Run golangci-lint
	golangci-lint run

test: ## Run unit tests
	go test -race ./...

test-integration: ## Run integration tests (requires Docker)
	go test -race -tags integration $(CMD)

coverage: ## Generate HTML coverage report
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

image: ## Build the container image via docker buildx bake
	docker buildx bake

clean: ## Remove build artifacts
	rm -f $(BINARY) coverage.out coverage.html

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  %-20s %s\n", $$1, $$2}'
