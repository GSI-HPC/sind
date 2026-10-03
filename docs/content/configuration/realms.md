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

Because names join realm and cluster with `-`, realm `ci` with cluster `42-dev` and realm `ci-42` with cluster `dev` would share the resource names `ci-42-dev-*`. sind refuses to create the second of them, and deleting one leaves the other's network and volumes alone.

## When to use realms

- **Parallel CI jobs** — each job uses a unique realm to avoid resource conflicts
- **Multiple environments** — run separate sets of clusters that don't interfere
- **Testing sind itself** — integration tests use random realms for isolation

## Setting the realm

Realm is determined by the following precedence (highest first):

| Source | Example |
|--------|---------|
| `--realm` flag | `sind --realm ci-42 create cluster` |
| `SIND_REALM` environment variable | `export SIND_REALM=ci-42` |
| Config file (`sind create cluster` only) | `realm: ci-42` in the YAML config |
| Default | `sind` |

Only `sind create cluster` reads a config file, so a `realm` set there applies to that command alone. Pass the same realm to the commands that follow, with `--realm` or `SIND_REALM`, or they look in another realm:

```bash
sind create cluster --config dev.yaml       # dev.yaml sets realm: ci-42
sind --realm ci-42 get cluster dev
sind --realm ci-42 delete cluster dev
```

When a command does not find a cluster in its realm, the error names the realms that hold a cluster of that name, for example `cluster "dev" not found in realm "sind" (it exists in realm "ci-42")`.

A realm name must be a single DNS label: lowercase ASCII letters, digits and `-`, 1 to 63 characters, not beginning or ending with `-` (for example `ci-42`, not `CI-42`, `ci_42` or `ci.42`). sind rejects an invalid realm from any source. An invalid `--realm` fails every command with exit status 2, even one that does not use a realm such as `sind version`; `SIND_REALM` is only checked when it is the realm in effect.

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

Mutating operations (`create cluster`, `delete cluster`, `create worker`, `delete worker`, and `power on`, `reboot` and `cycle`, which start the mesh and rewrite DNS records) acquire a per-realm file lock to prevent concurrent modifications. The lock file is stored at:

```
$XDG_STATE_HOME/sind/<realm>/lock    # default: ~/.local/state/sind/<realm>/lock
```

If another operation already holds the lock, sind prints `Warning: waiting for another sind command in realm "<realm>" to finish` to stderr and waits until it completes. Read-only operations (`get`, `logs`, etc.) are not affected.

Locks are per-realm — operations in different realms run concurrently without contention, making realm-based CI isolation safe for parallel jobs.

Go programs that use sind as a library take the same lock with `state.LockRealm` from `github.com/GSI-HPC/sind/pkg/state` around `cluster.Create`, `cluster.Delete`, `cluster.WorkerAdd` and `cluster.WorkerRemove`, which do not lock themselves.

The lock lives in the invoking user's state directory, while the realm's resources live on the Docker daemon. sind clients that share one daemon, such as several users of a host, or CI jobs that share the host's Docker socket, do not see each other's locks: give each of them its own realm.

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
