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

# A race-built program sleeps a second as it exits (atexit_sleep_ms), every
# test binary and every child that a test starts from it.
export GORACE ?= atexit_sleep_ms=0

# Sandbox for unit tests: blocks the host paths and service binaries the
# doctor/mesh/docker/ssh code paths would otherwise read or exec, so a test
# that forgets to inject a mock fs/executor trips a failure instead of
# silently touching the runner. /bin/* and /usr/bin/* are both listed for
# split-/usr portability; firejail ignores missing paths. A docker CLI found
# elsewhere on PATH reaches no daemon either: the daemon sockets, rootful,
# rootless and Docker Desktop's, are blocked, DOCKER_HOST and DOCKER_CONTEXT,
# which could name another, are removed, and DOCKER_CONFIG points at no
# config, so that no stored context (Colima, Podman, a remote daemon) is
# current.
RUNTIME_DIR   := $(or $(XDG_RUNTIME_DIR),/run/user/$(shell id -u))
FIREJAIL_TEST := firejail --quiet --noprofile \
	--rmenv=DOCKER_HOST --rmenv=DOCKER_CONTEXT --env=DOCKER_CONFIG=/nonexistent \
	--blacklist=/run/docker.sock --blacklist=/var/run/docker.sock \
	--blacklist=$(RUNTIME_DIR)/docker.sock \
	--blacklist=$(HOME)/.docker/desktop/docker.sock \
	--blacklist=/run/systemd \
	--blacklist=/bin/systemctl --blacklist=/usr/bin/systemctl \
	--blacklist=/bin/pkcheck --blacklist=/usr/bin/pkcheck \
	--blacklist=/bin/resolvectl --blacklist=/usr/bin/resolvectl \
	--blacklist=/bin/polkit-auth --blacklist=/usr/bin/polkit-auth \
	--blacklist=/bin/docker --blacklist=/usr/bin/docker \
	--blacklist=/bin/ssh --blacklist=/usr/bin/ssh

.PHONY: build install lint lint-ci audit-ci lint-docs check-docs docs-shots test fuzz mutation check-models test-integration coverage check-coverage image clean help

build: ## Build the sind binary
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) $(GOBUILD) -trimpath -ldflags='$(LDFLAGS)' -o $(BINARY) $(CMD)

install: ## Install sind to GOPATH/bin
	CGO_ENABLED=$(CGO_ENABLED) go install -trimpath -ldflags='$(LDFLAGS)' $(CMD)

lint: ## Run golangci-lint, on the unit and on the integration build
	golangci-lint run
	golangci-lint run --build-tags integration

lint-ci: ## Lint the workflows (actionlint) and the shell scripts (shellcheck)
	actionlint
	git ls-files -z '*.sh' | xargs -0 shellcheck

audit-ci: ## Audit the workflows and the Dependabot config for security defects (zizmor)
	zizmor .

lint-docs: ## Lint documentation markdown files
	npx markdownlint-cli2@0.24.1 "docs/content/**/*.md"

check-docs: ## Build the docs site in memory: a broken shortcode, template or page reference fails (requires Hugo)
	hugo --source docs --renderToMemory --panicOnWarning

docs-shots: build ## Take the sind ui screenshots of the docs into docs/static/ui-shots (needs Chromium)
	cd tools/screenshots && go run . -sind ../../$(BINARY) -out ../../docs/static/ui-shots

test: ## Run unit tests (sandboxed with firejail to catch host leaks)
	$(FIREJAIL_TEST) -- $(GOTEST) -race ./...

fuzz: ## Run every fuzz target for FUZZTIME each (default 30s)
	.github/scripts/fuzz.sh

mutation: ## Report the mutation score of MUTATION_PACKAGES (takes minutes; reports, does not fail on a score)
	.github/scripts/mutation.sh

check-models: ## Check the TLA+ models of spec/tla with TLC (requires Java)
	spec/tla/check.sh

test-integration: ## Run integration tests (requires Docker)
	$(GOTEST) -race -tags integration ./...

coverage: ## Generate HTML coverage report
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

check-coverage: ## Check coverage thresholds (requires go-test-coverage)
	$(MAKE) test GOTEST="go test -coverprofile=coverage.out"
	go-test-coverage --config .testcoverage.yml

image: ## Build the node images (one per Slurm release line) for the host platform via docker buildx bake
	docker buildx bake --allow=network.host --set '*.platform=local'

clean: ## Remove build artifacts
	rm -f sind-* coverage.out coverage.html

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  %-20s %s\n", $$1, $$2}'
