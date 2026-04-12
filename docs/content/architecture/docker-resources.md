---
weight: 430
title: "Docker Resources"
icon: "inventory_2"
description: "Resource naming conventions, volumes, and labels"
toc: true
---

## Per-cluster resources

| Type | Name pattern | Example (`sind create cluster dev`) |
|------|-------------|------------------------|
| Network | `<realm>-<cluster>-net` | `sind-dev-net` |
| Controller | `<realm>-<cluster>-controller` | `sind-dev-controller` |
| Backup controller | `<realm>-<cluster>-controller-backup` | `sind-dev-controller-backup` |
| Submitter | `<realm>-<cluster>-submitter` | `sind-dev-submitter` |
| Worker | `<realm>-<cluster>-worker-<N>` | `sind-dev-worker-0` |
| Config volume | `<realm>-<cluster>-config` | `sind-dev-config` |
| Munge volume | `<realm>-<cluster>-munge` | `sind-dev-munge` |
| Data volume | `<realm>-<cluster>-data` | `sind-dev-data` |

The default realm is `sind` and the default cluster name is `default`, resulting in prefixes like `sind-default-*`. See [Realms](../../configuration/realms/) for custom realm naming.

## Global resources (mesh)

| Type | Name pattern | Example |
|------|-------------|---------|
| Mesh network | `<realm>-mesh` | `sind-mesh` |
| DNS container | `<realm>-dns` | `sind-dns` |
| SSH container | `<realm>-ssh` | `sind-ssh` |
| SSH volume | `<realm>-ssh-config` | `sind-ssh-config` |

## Volume mounts

| Volume | Mount point | Controller | Worker | Submitter |
|--------|------------|------------|--------|-----------|
| `sind-<cluster>-config` | `/etc/slurm` | rw | ro | ro |
| `sind-<cluster>-munge` | `/etc/munge` | ro | ro | ro |
| `sind-<cluster>-data` | `/data` | rw | rw | rw |
| tmpfs | `/tmp` | configurable | configurable | configurable |
| tmpfs | `/run` | exec,mode=755 | exec,mode=755 | exec,mode=755 |
| tmpfs | `/run/lock` | — | — | — |

SELinux relabeling (`:z`) is not used because containers run with `--security-opt label=disable`. This avoids expensive recursive relabeling of bind-mounted host directories.

```
-v sind-dev-config:/etc/slurm:rw      # controller
-v sind-dev-config:/etc/slurm:ro      # all others
-v sind-dev-munge:/etc/munge:ro       # all nodes
-v sind-dev-data:/data:rw             # all nodes
--tmpfs /tmp:rw,nosuid,nodev,size=256m  # configurable size
```

### Host path storage

When `dataStorage.type: hostPath` is specified, the data volume is replaced with a bind mount:

```
-v /path/on/host:/data:rw
```

## Container labels

sind applies labels to containers for filtering and metadata:

| Label | Example | Description |
|-------|---------|-------------|
| `sind.realm` | `sind` | Realm namespace |
| `sind.cluster` | `dev` | Cluster name |
| `sind.role` | `worker` | Node role |
| `sind.slurm.version` | `25.11.4` | Slurm version |
| `sind.data.hostpath` | `/home/user/project` | Resolved data mount host path |
