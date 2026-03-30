---
weight: 610
title: "Development Setup"
icon: "code"
description: "Clone, build, and run sind locally"
toc: true
---

## Prerequisites

- **Go 1.21+**
- **Docker Engine 28.0+**
- **Linux host** with cgroupv2 and `nsdelegate` mount option

## Clone and build

```bash
git clone https://github.com/GSI-HPC/sind.git
cd sind
make build
```

The binary is built at `./sind`.

## Run

```bash
./sind --version
./sind create cluster
```

## Make targets

All common tasks are available via `make`:

| Target | Description |
|--------|-------------|
| `make build` | Build the sind binary |
| `make test` | Run unit tests with race detector |
| `make test-integration` | Run integration tests (requires Docker) |
| `make coverage` | Generate HTML coverage report |
| `make lint` | Run golangci-lint |
| `make lint-docs` | Lint documentation markdown files |
| `make image` | Build the container image via docker buildx bake |
| `make clean` | Remove build artifacts and coverage files |
| `make help` | Show all available targets |

The build version defaults to `dev` and can be overridden:

```bash
make build VERSION=v1.0.0
```

## Dependencies

sind uses a minimal set of dependencies:

| Dependency | Purpose |
|------------|---------|
| `github.com/spf13/cobra` | CLI framework |
| `sigs.k8s.io/yaml` | YAML configuration parsing |
| `github.com/mattn/go-isatty` | TTY detection for interactive commands |
| `github.com/spf13/afero` | Filesystem abstraction for testability |
| `golang.org/x/sync` | Errgroup for concurrent operations |
| `github.com/stretchr/testify` | Test assertions (test only) |

## Run tests

Unit tests (no Docker required):

```bash
make test
```

Integration tests (requires Docker):

```bash
make test-integration
```

See [Testing](../testing/) for details on the test infrastructure.
