---
weight: 220
title: "Node Definitions"
icon: "dns"
description: "Node roles, shorthand syntax, managed vs unmanaged workers, database and API nodes, and unmanaged clusters"
toc: true
---

## Node roles

| Role | Count | Required | Slurm daemons | Description |
|------|-------|----------|---------------|-------------|
| `controller` | exactly 1 | yes | slurmctld | Cluster controller |
| `db` | 0–1 | no | mariadb, slurmdbd | Accounting database (see below) |
| `api` | 0–1 | no | slurmrestd | REST API (see below) |
| `submitter` | 0–1 | no | none (clients only; `sackd` with identity `clientIds`) | Job submission node |
| `worker` | 1+ | yes | slurmd | Worker nodes |

## Node parameters

| Parameter | Scope | Default | Description |
|-----------|-------|---------|-------------|
| `image` | global + per-node | the image of the sind release, e.g. `ghcr.io/gsi-hpc/sind-node:v0.11.0` ([details]({{< relref "/container-images/building-images#official-images" >}})) | Container image |
| `cpus` | global + per-node | `1` | CPU limit; a managed worker's Slurm `CPUs` |
| `memory` | global + per-node | `"512m"` | Memory limit, without swap, in Docker's size syntax (`2g`, `2gb`, `1.5GiB`, ...), for the jobs, the node's own services and its `/tmp`, `/run` and `/dev/shm` files; `/dev/shm` gets half of it; a managed worker's Slurm `RealMemory` |
| `tmpSize` | global + per-node | `"256m"` | tmpfs size for `/tmp`, a whole number with an optional unit or a percentage; files there count against `memory` |
| `count` | worker only | `1` | Number of worker nodes |
| `managed` | controller + db + api + worker | `true` | Worker: start slurmd and add to slurm.conf. Db: run MariaDB and slurmdbd and configure accounting (see below). Api: run slurmrestd and set up JWT (see below). Controller: `false` makes the whole cluster unmanaged (see below) |
| `backupController` | controller only | `false` | Add a backup controller, `controller-backup` (see below) |
| `capAdd` | global + per-node | none | Extra Linux capabilities (e.g. `SYS_ADMIN`) |
| `capDrop` | global + per-node | none | Dropped Linux capabilities |
| `devices` | global + per-node | none | Host devices to expose (e.g. `/dev/fuse`) |
| `securityOpt` | global + per-node | none | Extra security options |

Per-node scalar values override the `defaults` section. Security list fields (`capAdd`, `capDrop`, `devices`, `securityOpt`) are **merged** with defaults rather than replacing them.

## Shorthand syntax

Nodes can be specified in a compact form when only role and count are needed:

```yaml
nodes:
  - controller               # bare role string
  - submitter
  - worker: 3                # role with count
```

This is equivalent to:

```yaml
nodes:
  - role: controller
  - role: submitter
  - role: worker
    count: 3
```

Shorthand and full forms can be mixed in the same configuration.

## Managed vs unmanaged workers

By default, workers are **managed**: sind starts slurmd and adds the node to `sind-nodes.conf` so Slurm knows about it. This is the typical setup.

**Unmanaged** workers (`managed: false`) are created as containers but sind does not start slurmd and does not add them to the Slurm configuration. This is useful for:

- Testing manual node registration workflows
- Simulating nodes that join the cluster later
- Running custom Slurm configurations

```yaml
nodes:
  - role: worker
    count: 3              # 3 managed workers

  - role: worker
    count: 2
    managed: false        # 2 unmanaged workers
```

Unmanaged workers can also be created dynamically:

```bash
sind create worker --count 2 --unmanaged
```

In an [unmanaged cluster](#unmanaged-cluster) every worker is unmanaged.

## Backup controller

`backupController: true` on the controller node adds a second controller, `controller-backup`, and configures Slurm's active/passive controller pair:

```yaml
nodes:
  - role: controller
    backupController: true
  - role: worker
    count: 2
```

- `controller-backup` uses the same image, resources, volumes, capabilities, devices and security options as `controller`.
- `slurm.conf` lists both hosts (`SlurmctldHost=controller`, then `SlurmctldHost=controller-backup`) and sets `SlurmctldTimeout=20` unless the `main` section sets it.
- Both controllers mount the shared state volume `<realm>-<cluster>-state` at `/var/spool/slurmctld` (`StateSaveLocation`).
- `slurmctld` runs on both. The backup takes over when the primary stops responding for `SlurmctldTimeout` seconds, and hands control back when the primary's `slurmctld` starts again.

`SlurmctldHost` and `StateSaveLocation` cannot be set in `slurm.main` when the backup is enabled. See [Controller Failover]({{< relref "/guides/controller-failover" >}}) for checking which controller is in control and triggering a failover.

## Database node

A `db` node runs MariaDB and slurmdbd, so the cluster records job accounting:

```yaml
nodes:
  - controller
  - db
  - worker: 2
```

- The container is `<realm>-<cluster>-db`, reachable as `db` inside the cluster. `count` and `backupController` are not valid on it.
- sind writes `slurmdbd.conf` (owned by `slurm`, mode `0600`) and adds `AccountingStorageType=accounting_storage/slurmdbd`, `AccountingStorageHost=db` and `JobAcctGatherType=jobacct_gather/cgroup` to `slurm.conf`, each unless the `main` section sets it. Extra slurmdbd settings go in the [`slurmdbd` section]({{< relref "/configuration/cluster-config#slurm-section" >}}); in its map form, the `slurmdbd.conf.d` fragments are protected the same way.
- At creation, sind starts MariaDB, creates the `slurm_acct_db` database and the `slurm` database user, and starts slurmdbd before slurmctld and slurmd, so the controller registers the cluster on startup: `sind exec dev -- sacctmgr show cluster` lists it, and `sacct` shows finished jobs.
- MariaDB authenticates the `slurm` database user by `unix_socket`: only slurmdbd, running as the OS user `slurm`, can log in as it, not the cluster users. With a `StoragePass` in the `slurmdbd` section, the user gets that password instead.
- Accounting is not enforced: jobs run without users, accounts or associations. Declare them in the [`accounts` section]({{< relref "/configuration/cluster-config#accounts-section" >}}), or add them with `sacctmgr`, and set `AccountingStorageEnforce` in the `main` section to test limits.
- `sind get cluster` and `sind get node` report `mariadb` and `slurmdbd` for the db node.
- `managed: false` on the db node makes it a bare node in an otherwise managed cluster, to test your own slurmdbd provisioning while sind runs slurmctld and slurmd. sind then configures no accounting: no `slurmdbd.conf` (the `slurmdbd` section is rejected), no database, and no accounting parameters in `slurm.conf`. Point slurmctld at your slurmdbd through the `main` section, for example `AccountingStorageType=accounting_storage/slurmdbd` and `AccountingStorageHost=db`.
- In an unmanaged cluster the db node is a bare node: sind starts neither MariaDB nor slurmdbd and writes no `slurmdbd.conf`.

Each cluster has its own db node; clusters do not share a slurmdbd.

## API node

An `api` node runs slurmrestd, Slurm's REST API daemon, with JWT authentication:

```yaml
nodes:
  - controller
  - db
  - api
  - worker: 2
```

- The container is `<realm>-<cluster>-api`, reachable as `api` inside the cluster. `count` and `backupController` are not valid on it. It needs a Slurm 26.05 image: the 25.11 images have no slurmrestd, and sind fails the create with the image's name.
- slurmrestd cannot unshare namespaces in a container, which it does by default at startup: sind adds the systemd drop-in `/etc/systemd/system/slurmrestd.service.d/sind.conf`, which sets `SLURMRESTD_SECURITY=disable_unshare_sysv,disable_unshare_files`.
- slurmrestd listens on port 6820: `http://api.<cluster>.<realm>.sind:6820`, e.g. `http://api.default.sind.sind:6820/slurm/v0.0.45/ping/`, from the host or any container on the cluster network. sind publishes no host port.
- sind writes `jwt_hs256.key` (owned by `slurm`, mode `0600`) to `/etc/slurm` and adds `AuthAltTypes=auth/jwt` and `AuthAltParameters=jwt_key=/etc/slurm/jwt_hs256.key` to `slurm.conf`, and to `slurmdbd.conf` with a db node, each unless the `main` (or `slurmdbd`) section sets it. With identity `clientIds`, `AuthAltParameters` also lists `use_jwt_client_ids`.
- Each request needs a token: `sind exec dev -- scontrol token username=root` prints one for root. See [REST API]({{< relref "/guides/rest-api" >}}) for tokens, users and identity modes.
- `sind get cluster` and `sind get node` report `slurmrestd` for the api node.
- `managed: false` on the api node makes it a bare node in an otherwise managed cluster, to test your own slurmrestd provisioning: sind starts no slurmrestd and sets up no JWT. In an unmanaged cluster the api node is a bare node too.

## Unmanaged cluster

`managed: false` on the controller makes the whole cluster unmanaged: sind creates the nodes, the volumes and the munge key, but writes no Slurm configuration and starts no Slurm daemon, so your own tooling can provision Slurm.

```yaml
nodes:
  - role: controller
    managed: false
  - role: worker
    count: 2
```

- Every worker is unmanaged. A worker with `managed: true` is rejected, and so is any `slurm` section.
- `backupController: true` still adds `controller-backup` and the shared state volume.
- `managed` is not valid on the submitter, which runs no Slurm daemon. db and api nodes are bare too, and `managed: true` on them is rejected.

See [Unmanaged Cluster]({{< relref "/guides/unmanaged-cluster" >}}) for provisioning Slurm on such a cluster.

## Capabilities and devices

sind's default security posture avoids extra capabilities and device access. When specific use cases require them (e.g. testing [CVMFS provisioning]({{< relref "/guides/cvmfs#provision-cvmfs-inside-the-nodes" >}}) or FUSE-based filesystems), you can grant targeted privileges per node.

```yaml
nodes:
  - role: controller
  - role: worker
    count: 3
    capAdd:
      - SYS_ADMIN
    devices:
      - /dev/fuse
```

Capability names follow Docker convention (without the `CAP_` prefix). Device strings use Docker's format: `/dev/fuse` or `/dev/sda:/dev/xvda:rwm`.

These fields can give a node root access to the host, so a config that sets them is only as safe as its author; see [Trust]({{< relref "/configuration/cluster-config#trust" >}}).

When set in the `defaults` section, security fields apply to all nodes. Per-node values merge with (not replace) defaults:

```yaml
defaults:
  capAdd:
    - SYS_ADMIN
  devices:
    - /dev/fuse

nodes:
  - role: controller
  - role: worker
    count: 3
    capAdd:
      - NET_ADMIN    # workers get both SYS_ADMIN and NET_ADMIN
```

With `-v`, `sind create cluster` and `sind create worker` log an `extra privileges` notice at the info level for each node that gets extra capabilities, devices or security options, or bind-mounts host directories (the data directory, the host's `/cvmfs`). Without `-v` it is not shown: it is no warning, and a creation that succeeds prints nothing but, on a terminal, the lines its [progress display]({{< relref "/usage/progress" >}}) leaves.

`securityOpt` entries must name an option Docker knows (`label`, `apparmor`, `seccomp`, `no-new-privileges`, `writable-cgroups` or `systempaths`); sind rejects others before it creates any container, and Docker checks the values.

One capability sind adds itself, and only when the [`main` section]({{< relref "/configuration/cluster-config#slurm-section" >}}) sets a `TaskPlugin` with `task/affinity`: managed workers then get `SYS_NICE`, including workers added later with `sind create worker`. slurmstepd binds each task to its CPUs after the task has become the job's user, and setting the CPU affinity of another user's process needs `CAP_SYS_NICE`, which Docker drops by default. Without it, every job of a user other than root fails with `task_g_set_affinity` and "Slurmd could not execve job". sind's default, `TaskPlugin=task/cgroup`, binds no tasks and needs no extra capability (see [Slurm Configuration]({{< relref "/architecture/slurm-config#slurmconf" >}})). A `capDrop` of `SYS_NICE` or `ALL` keeps the capability off, as `--cap-drop` would not undo sind's `--cap-add`. For an unmanaged cluster whose own `slurm.conf` uses `task/affinity`, add `SYS_NICE` to the workers' `capAdd`; workers added later with `sind create worker` inherit it, as they do every capability sind did not add itself.

Workers created via `sind create worker` take these fields from the cluster's newest worker, and the flags replace them:

```bash
sind create worker --cap-add SYS_ADMIN --device /dev/fuse
```

See [Worker Management]({{< relref "/usage/worker-management#defaults-from-the-newest-worker" >}}) for what new workers inherit.

## Default nodes

When the `nodes` section is omitted entirely, sind creates a minimal cluster:

```yaml
nodes:
  - role: controller
  - role: worker
```
