---
weight: 430
title: "Docker Resources"
icon: "inventory_2"
description: "Resource naming conventions, volumes, and labels"
toc: true
---

## Per-cluster resources

| Type | Name pattern | Example (`--name dev`) |
|------|-------------|------------------------|
| Network | `sind-<cluster>-net` | `sind-dev-net` |
| Controller | `sind-<cluster>-controller` | `sind-dev-controller` |
| Submitter | `sind-<cluster>-submitter` | `sind-dev-submitter` |
| Worker | `sind-<cluster>-worker-<N>` | `sind-dev-worker-0` |
| Config volume | `sind-<cluster>-config` | `sind-dev-config` |
| Munge volume | `sind-<cluster>-munge` | `sind-dev-munge` |
| Data volume | `sind-<cluster>-data` | `sind-dev-data` |

When `--name` is omitted, the default cluster name is `default`, resulting in prefixes like `sind-default-*`.

## Global resources (mesh)

| Type | Name |
|------|------|
| Mesh network | `sind-mesh` |
| DNS container | `sind-dns` |
| SSH container | `sind-ssh` |
| SSH volume | `sind-ssh-config` |

## Volume mounts

| Volume | Mount point | Controller | Worker | Submitter |
|--------|------------|------------|--------|-----------|
| `sind-<cluster>-config` | `/etc/slurm` | rw | ro | ro |
| `sind-<cluster>-munge` | `/etc/munge` | ro | ro | ro |
| `sind-<cluster>-data` | `/data` | rw | rw | rw |
| tmpfs | `/tmp` | configurable | configurable | configurable |
| tmpfs | `/run` | exec,mode=755 | exec,mode=755 | exec,mode=755 |
| tmpfs | `/run/lock` | — | — | — |

All volume mounts include the `:z` SELinux label for compatibility with SELinux-enabled systems:

```
-v sind-dev-config:/etc/slurm:rw,z    # controller
-v sind-dev-config:/etc/slurm:ro,z    # all others
-v sind-dev-munge:/etc/munge:ro,z     # all nodes
-v sind-dev-data:/data:rw,z           # all nodes
--tmpfs /tmp:rw,nosuid,nodev,size=1g  # configurable size
```

### Host path storage

When `dataStorage.type: hostPath` is specified, the data volume is replaced with a bind mount:

```
-v /path/on/host:/data:rw,z
```

## Container labels

sind applies labels to containers for filtering and metadata:

| Label | Example | Description |
|-------|---------|-------------|
| `sind.cluster` | `dev` | Cluster name |
| `sind.role` | `worker` | Node role |
| `sind.slurm.version` | `25.11.4` | Slurm version |
