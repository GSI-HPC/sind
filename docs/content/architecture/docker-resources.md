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
| State volume (backup controller only) | `<realm>-<cluster>-state` | `sind-dev-state` |

The default realm is `sind` and the default cluster name is `default`, resulting in prefixes like `sind-default-*`. See [Realms](../../configuration/realms/) for custom realm naming.

## Global resources (mesh)

| Type | Name pattern | Example | Image |
|------|-------------|---------|-------|
| Mesh network | `<realm>-mesh` | `sind-mesh` | — |
| DNS container | `<realm>-dns` | `sind-dns` | `coredns/coredns:latest` |
| SSH container | `<realm>-ssh` | `sind-ssh` | `ghcr.io/gsi-hpc/sind-node:latest` (runs `sleep infinity`) |
| SSH volume | `<realm>-ssh-config` | `sind-ssh-config` | written once by a `busybox:latest` helper, `<realm>-ssh-keygen` |

The mesh images do not follow `defaults.image`: the SSH relay always runs `sind-node:latest`, and CoreDNS and BusyBox come from Docker Hub. `--pull` pulls them too.

## Volume mounts

| Volume | Mount point | Controller | Worker | Submitter |
|--------|------------|------------|--------|-----------|
| `<realm>-<cluster>-config` | `/etc/slurm` | rw | ro | ro |
| `<realm>-<cluster>-munge` | `/etc/munge` | ro | ro | ro |
| `<realm>-<cluster>-data` | `/data` | rw | rw | rw |
| `<realm>-<cluster>-state` | `/var/spool/slurmctld` | rw (backup controller pairs only) | — | — |
| tmpfs | `/tmp` | configurable | configurable | configurable |
| tmpfs | `/run` | exec,mode=755 | exec,mode=755 | exec,mode=755 |
| tmpfs | `/run/lock` | — | — | — |

Unmanaged clusters use the same mounts; their config volume starts empty.

SELinux relabeling (`:z`) is not used because containers run with `--security-opt label=disable`. This avoids expensive recursive relabeling of bind-mounted host directories.

```
-v sind-dev-config:/etc/slurm:rw      # controller
-v sind-dev-config:/etc/slurm:ro      # all others
-v sind-dev-munge:/etc/munge:ro       # all nodes
-v sind-dev-data:/data:rw             # all nodes
-v sind-dev-state:/var/spool/slurmctld:rw  # both controllers of a backup pair
--tmpfs /tmp:rw,nosuid,nodev,size=256m  # configurable size
```

### Host path storage

With `dataStorage.type: hostPath`, or by default through `sind create cluster --data .` when the config sets no `dataStorage`, the data volume is replaced with a bind mount:

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
| `sind.managed` | `true` | Whether sind manages Slurm on the node: `false` for unmanaged workers and for every node of an unmanaged cluster. Nodes created before this label existed count as managed. |
| `sind.slurm.version` | `25.11.8` | Slurm version |
| `sind.data.hostpath` | `/home/user/project` | Resolved data mount host path |
| `sind.data.mountpath` | `/shared` | Data mount point, when `storage.dataStorage.mountPath` is not `/data` |

A cluster's network and volumes carry `sind.realm` and `sind.cluster`; the mesh network and the SSH volume carry `sind.realm` only. `sind get networks`, `sind get volumes` and `sind delete cluster --all` find resources by these labels.

Every container, network and volume also carries Docker Compose labels, so Compose-aware tools group them: the project is `<realm>-<cluster>` (`<realm>-mesh` for the mesh), the service is the node's role (`dns` or `ssh` in the mesh), the container number is 1, or N+1 for `worker-N` and 2 for `controller-backup`, and networks and volumes name themselves `net`, `mesh`, the volume type or `ssh-config`.
