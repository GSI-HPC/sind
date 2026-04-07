# SPDX-License-Identifier: LGPL-3.0-or-later

CMD           := ./cmd/sind
VERSION       ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT        ?= $(shell git rev-parse --short HEAD 2>/dev/null)
GOOS          ?= $(shell go env GOOS)
GOARCH        ?= $(shell go env GOARCH)
BINARY        := sind-$(GOOS)-$(GOARCH)
LDFLAGS       := -X main.version=$(VERSION) -X main.commit=$(COMMIT)
CGO_ENABLED   ?= 0
GOBUILD       ?= go build
GOTEST        ?= go test

.PHONY: build lint lint-docs test test-integration coverage image clean help

build: ## Build the sind binary
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) $(GOBUILD) -trimpath -ldflags='$(LDFLAGS)' -o $(BINARY) $(CMD)

lint: ## Run golangci-lint
	golangci-lint run

lint-docs: ## Lint documentation markdown files
	npx markdownlint-cli2 "docs/content/**/*.md"

test: ## Run unit tests
	$(GOTEST) -race ./...

test-integration: ## Run integration tests (requires Docker)
	$(GOTEST) -race -tags integration ./...

coverage: ## Generate HTML coverage report
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

check-coverage: ## Check coverage thresholds (requires go-test-coverage)
	go test -race -coverprofile=coverage.out ./...
	go-test-coverage --config .testcoverage.yml

image: ## Build the container image via docker buildx bake
	docker buildx bake

clean: ## Remove build artifacts
	rm -f sind-* coverage.out coverage.html

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  %-20s %s\n", $$1, $$2}'
