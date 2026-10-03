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
| Db | `<realm>-<cluster>-db` | `sind-dev-db` |
| Submitter | `<realm>-<cluster>-submitter` | `sind-dev-submitter` |
| Worker | `<realm>-<cluster>-worker-<N>` | `sind-dev-worker-0` |
| Config volume | `<realm>-<cluster>-config` | `sind-dev-config` |
| Munge volume (not with identity `clientIds`) | `<realm>-<cluster>-munge` | `sind-dev-munge` |
| Data volume | `<realm>-<cluster>-data` | `sind-dev-data` |
| State volume (backup controller only) | `<realm>-<cluster>-state` | `sind-dev-state` |
| Home volume (`users` only) | `<realm>-<cluster>-home` | `sind-dev-home` |
| Config helper (temporary, managed clusters only) | `<realm>-<cluster>-config-helper` | `sind-dev-config-helper` |
| Munge helper (temporary, not with identity `clientIds`) | `<realm>-<cluster>-munge-helper` | `sind-dev-munge-helper` |

The helpers mount the config and munge volumes while `sind create cluster` writes the Slurm configuration and the munge key into them, using the controller's image, and are removed once the files are written. They carry the `sind.realm` and `sind.cluster` labels, so `sind delete cluster` removes one that an interrupted create left behind (`docker ps -a`).

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

| Volume | Mount point | Controller | Db | Worker | Submitter |
|--------|------------|------------|----|--------|-----------|
| `<realm>-<cluster>-config` | `/etc/slurm` | rw | ro | ro | ro |
| `<realm>-<cluster>-munge` | `/etc/munge` | ro | ro | ro | ro (not with identity `clientIds`) |
| `<realm>-<cluster>-data` | `/data` | rw | rw | rw | rw |
| `<realm>-<cluster>-state` | `/var/spool/slurmctld` | rw (backup controller pairs only) | — | — | — |
| `<realm>-<cluster>-home` | `/home` | rw | rw | rw | rw (`users` only) |
| `cvmfs` plugin volume or host `/cvmfs` | `/cvmfs` | ro | ro | ro | ro (`storage.cvmfs` only) |
| tmpfs | `/tmp` | configurable | configurable | configurable | configurable |
| tmpfs | `/run` | exec,mode=755 | exec,mode=755 | exec,mode=755 | exec,mode=755 |
| tmpfs | `/run/lock` | — | — | — | — |

Unmanaged clusters use the same mounts; their config volume starts empty.

SELinux relabeling (`:z`) is not used because containers run with `--security-opt label=disable`. This avoids expensive recursive relabeling of bind-mounted host directories.

```
-v sind-dev-config:/etc/slurm:rw      # controller
-v sind-dev-config:/etc/slurm:ro      # all others
-v sind-dev-munge:/etc/munge:ro       # all nodes
-v sind-dev-data:/data:rw             # all nodes
-v sind-dev-state:/var/spool/slurmctld:rw  # both controllers of a backup pair
-v sind-dev-home:/home:rw             # all nodes, with users
--tmpfs /tmp:rw,nosuid,nodev,size=256m  # configurable size
```

### Host path storage

With `dataStorage.type: hostPath`, or by default through `sind create cluster --data .` when the config sets no `dataStorage`, the data volume is replaced with a bind mount:

```
-v /path/on/host:/data:rw
```

### CVMFS

With `storage.cvmfs: true`, every node mounts CVMFS read-only at `/cvmfs`, from the `cvmfs` Docker volume plugin when it is installed and enabled, otherwise from the Docker host's `/cvmfs` as a slave mount, so repositories the host's autofs mounts on demand appear in the nodes:

```
--mount type=volume,volume-driver=cvmfs,source=cvmfs,target=/cvmfs,readonly     # volume plugin
--mount type=bind,source=/cvmfs,target=/cvmfs,readonly,bind-propagation=rslave  # host /cvmfs
```

All clusters share the plugin's `cvmfs` volume; sind does not create or remove it. See [Using CVMFS]({{< relref "/guides/cvmfs" >}}).

## Container labels

sind applies labels to containers for filtering and metadata:

| Label | Example | Description |
|-------|---------|-------------|
| `sind.realm` | `sind` | Realm namespace |
| `sind.cluster` | `dev` | Cluster name |
| `sind.role` | `worker` | Node role |
| `sind.managed` | `true` | Whether sind manages Slurm on the node: `false` for unmanaged workers and db nodes and for every node of an unmanaged cluster. Nodes created before this label existed count as managed. |
| `sind.slurm.version` | `25.11.8` | Slurm version, empty for an unmanaged cluster |
| `sind.data.hostpath` | `/home/user/project` | Resolved data mount host path, empty with the data volume |
| `sind.data.mountpath` | `/shared` | Data mount point (`storage.dataStorage.mountPath`, `/data` by default) |
| `sind.cvmfs` | `hostPath` | How the node mounts CVMFS, with `storage.cvmfs`: `volume` (plugin) or `hostPath` (host `/cvmfs`); empty without it |
| `sind.users` | `alice:1000:1000 bob:2001:3000` | The cluster users, as space-separated `name:uid:gid` entries; empty without `users` |
| `sind.groups` | `alice:1000 hpc:3000:carol` | The cluster groups, private groups included, as space-separated `name:gid` entries with `:member+member...` for supplementary members; empty without `users` and `groups` |
| `sind.identity` | `clientIds` | The identity mode: `local`, `nssSlurm` or `clientIds` |

Every node container gets all of these labels, empty where there is nothing to record. Docker merges the image's labels into the container's, so a label sind left out could otherwise come from the node image.

A cluster's network and volumes carry `sind.realm` and `sind.cluster`; the mesh network and the SSH volume carry `sind.realm` only. `sind get networks`, `sind get volumes` and `sind delete cluster --all` find resources by these labels.

Every node container, the mesh's DNS and SSH containers, and every network and volume also carry Docker Compose labels, so Compose-aware tools group them: the project is `<realm>-<cluster>` (`<realm>-mesh` for the mesh), the service is the node's role (`dns` or `ssh` in the mesh), the container number is 1, or N+1 for `worker-N` and 2 for `controller-backup`, and networks and volumes name themselves `net`, `mesh`, the volume type or `ssh-config`.
