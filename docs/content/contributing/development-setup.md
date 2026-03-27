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
go build -o sind ./cmd/sind/
```

The binary is built at `./sind`.

## Run

```bash
./sind --version
./sind create cluster
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
go test ./...
```

Integration tests (requires Docker):

```bash
go test -tags integration ./...
```

See [Testing](../testing/) for details on the test infrastructure.
