---
weight: 310
title: "Cluster Lifecycle"
icon: "cycle"
description: "Creating, listing, and deleting clusters"
toc: true
---

## Create a cluster

```bash
sind create cluster [NAME] [--config FILE] [--pull]
```

| Argument/Flag | Default | Description |
|---------------|---------|-------------|
| `NAME` | `default` | Cluster name (positional, optional) |
| `--config` | — | Path to YAML configuration file |
| `--pull` | `false` | Pull images before creating containers |

Without `--config`, sind creates a minimal cluster (1 controller + 1 worker) using the default image. Configuration can also be piped via stdin instead of using `--config`.

When both a positional `NAME` and a config file with a `name:` field are provided, the positional argument takes precedence.

```bash
# Minimal cluster (named "default")
sind create cluster

# Named cluster
sind create cluster dev

# From config file
sind create cluster --config cluster.yaml

# Config file with name override
sind create cluster staging --config cluster.yaml

# Pipe config from stdin
sind create cluster << 'EOF'
kind: Cluster
name: dev
nodes:
  - controller
  - worker: 3
EOF
```

### What happens during creation

1. Mesh infrastructure is created if not already present (network, DNS, SSH)
2. Cluster network and volumes are created
3. Munge key and Slurm configuration are generated
4. All node containers start in parallel
5. sind waits for each node to become ready (systemd, sshd, Slurm daemons)

If any node fails to become ready within the timeout, the command fails. The partial cluster is not cleaned up automatically — use `sind delete cluster` to remove it.

### Preflight checks

Before creating resources, sind checks for conflicts — containers, networks, or volumes with matching names that already exist. If conflicts are found, creation fails with an error.

## List clusters

```bash
sind get clusters
```

```
NAME      NODES (S/C/W)   SLURM    STATUS
default   4 (1/1/2)       25.11    running
dev       3 (0/1/2)       25.11    running
```

The `NODES` column shows the total count and breakdown: **S**ubmitter / **C**ontroller / **W**orker.

## Delete a cluster

```bash
sind delete cluster [NAME]
```

Deleting is idempotent — deleting a non-existent cluster is not an error. sind handles partial or broken clusters (e.g., from a failed creation).

Deletion order: stops/removes containers, disconnects/removes networks, removes volumes. sind also updates `~/.local/state/sind/<realm>/known_hosts` (or `$XDG_STATE_HOME/sind/<realm>/known_hosts`) to remove deleted nodes.

```bash
# Delete the default cluster
sind delete cluster

# Delete a named cluster
sind delete cluster dev

# Delete all clusters
sind delete cluster --all
```

When the last cluster is deleted, sind also removes the shared mesh infrastructure (DNS, SSH, mesh network).
