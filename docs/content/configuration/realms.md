---
weight: 230
title: "Realms"
icon: "shield"
description: "Isolated mesh namespaces for parallel environments"
toc: true
---

## Overview

A **realm** is a namespace that isolates all sind resources — mesh network, DNS, SSH, and cluster resources. Different realms have completely separate infrastructure and cannot see each other.

The default realm is `sind`, which produces resource names like `sind-mesh`, `sind-dns`, `sind-default-net`, etc.

## When to use realms

- **Parallel CI jobs** — each job uses a unique realm to avoid resource conflicts
- **Multiple environments** — run separate sets of clusters that don't interfere
- **Testing sind itself** — integration tests use random realms for isolation

## Setting the realm

Realm is determined by the following precedence (highest first):

| Source | Example |
|--------|---------|
| `--realm` flag | `sind --realm ci-42 create cluster` |
| Config file | `realm: ci-42` in the YAML config |
| `SIND_REALM` environment variable | `export SIND_REALM=ci-42` |
| Default | `sind` |

## Resource naming

With realm `ci-42`, resources are prefixed accordingly:

| Resource | Default realm (`sind`) | Custom realm (`ci-42`) |
|----------|----------------------|----------------------|
| Mesh network | `sind-mesh` | `ci-42-mesh` |
| DNS container | `sind-dns` | `ci-42-dns` |
| SSH container | `sind-ssh` | `ci-42-ssh` |
| Cluster network | `sind-default-net` | `ci-42-default-net` |
| Container | `sind-default-controller` | `ci-42-default-controller` |

## Advisory locking

Mutating operations (`create cluster`, `delete cluster`, `create worker`, `delete worker`) acquire a per-realm file lock to prevent concurrent modifications. The lock file is stored at:

```
~/.local/state/sind/<realm>/lock
```

If another operation already holds the lock, sind waits until it completes. Read-only operations (`get`, `logs`, etc.) are not affected.

Locks are per-realm — operations in different realms run concurrently without contention, making realm-based CI isolation safe for parallel jobs.

## Example

```bash
# Create two isolated environments
sind --realm dev create cluster app
sind --realm staging create cluster app

# Each has its own mesh, DNS, and clusters
sind --realm dev get clusters
sind --realm staging get clusters

# Tear down independently
sind --realm dev delete cluster app
sind --realm staging delete cluster app
```
