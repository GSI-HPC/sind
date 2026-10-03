---
weight: 310
title: "Cluster Lifecycle"
icon: "cycle"
description: "Creating, listing, and deleting clusters"
toc: true
---

## Create a cluster

```bash
sind create cluster [NAME] [--config FILE] [--pull] [--data PATH|volume] [--wait DURATION]
```

| Argument/Flag | Default | Description |
|---------------|---------|-------------|
| `NAME` | `default` | Cluster name (positional, optional) |
| `--config` | — | Path to YAML configuration file |
| `--pull` | `false` | Pull images before creating containers |
| `--data` | `.` | Host directory to mount at `/data` on all nodes, or `volume` to use a Docker volume. Ignored if the config sets `storage.dataStorage.type` or `hostPath`. |
| `--wait` | `5m` | How long to wait for the nodes and Slurm to become ready, counted from when the node containers have started; `0` for no limit |

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

`--wait` limits how long sind waits for the nodes, the Slurm daemons and, with `accounts`, for slurmdbd to register the cluster. Each node's limit counts from when its container has started, so pulling the image does not count against it. A container that exits, or a munge, slurmctld, slurmd, sackd or slurmdbd unit that fails, ends the wait at once with the tail of the unit's journal.

If a node is not ready within `--wait`, or a check fails for good, the command fails with exit status 1 and names the node and the last check that failed, for example:

```text
ERRO cluster dev not ready within 5m0s: waiting for worker-0: node sind-dev-worker-0 not ready: context deadline exceeded; last probe error: probe munge: munge not ready: activating
```

sind then removes the resources it created, including a mesh that this invocation set up. If that cleanup fails too, use `sind delete cluster` to remove what is left. On a slow host or CI runner, raise the limit, e.g. `--wait 15m`, or use `--wait 0` to wait until interrupted.

Ctrl-C (SIGINT) or SIGTERM, as sent by `timeout` or `docker stop`, stops the creation and runs the same cleanup; sind then exits with status 130 (see [Exit Status]({{< relref "/usage/exit-status" >}})). A second signal ends sind at once, without waiting for the cleanup to finish.

### Preflight checks

Before it sets up the mesh or pulls an image, sind asks the Docker daemon whether it can start sind's nodes: a daemon in rootless mode or with `userns-remap` refuses their writable cgroups, and one that runs containers on cgroup v1 cannot boot them, so creation fails at once with an error that says so.

Before creating resources, sind checks for conflicts — containers, networks, or volumes with matching names that already exist. If conflicts are found, creation fails with an error.

## List clusters

```bash
sind get clusters
```

```
NAME      NODES (S/C/D/W)   SLURM     STATUS
default   4 (1/1/0/2)       26.05.4   running
dev       4 (0/1/1/2)       25.11.8   running
```

The `NODES` column shows the total count and breakdown: **S**ubmitter / **C**ontroller / **D**b / **W**orker. `SLURM` shows `-` when sind does not know the version, as for unmanaged clusters.

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
