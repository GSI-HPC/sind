---
weight: 220
title: "Node Definitions"
icon: "dns"
description: "Node roles, shorthand syntax, managed vs unmanaged workers, database nodes, and unmanaged clusters"
toc: true
---

## Node roles

| Role | Count | Required | Slurm daemons | Description |
|------|-------|----------|---------------|-------------|
| `controller` | exactly 1 | yes | slurmctld | Cluster controller |
| `db` | 0–1 | no | mariadb, slurmdbd | Accounting database (see below) |
| `submitter` | 0–1 | no | none (clients only) | Job submission node |
| `worker` | 1+ | yes | slurmd | Worker nodes |

## Node parameters

| Parameter | Scope | Default | Description |
|-----------|-------|---------|-------------|
| `image` | global + per-node | `ghcr.io/gsi-hpc/sind-node:latest` | Container image |
| `cpus` | global + per-node | `1` | CPU limit |
| `memory` | global + per-node | `"512m"` | Memory limit |
| `tmpSize` | global + per-node | `"256m"` | tmpfs size for `/tmp` |
| `count` | worker only | `1` | Number of worker nodes |
| `managed` | controller + db + worker | `true` | Worker: start slurmd and add to slurm.conf. Db: run MariaDB and slurmdbd and configure accounting (see below). Controller: `false` makes the whole cluster unmanaged (see below) |
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
- `managed` is not valid on the submitter, which runs no Slurm daemon. A db node is bare too, and `managed: true` on it is rejected.

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

sind logs a notice at cluster creation when extra privileges are configured, making the escalation visible.

One capability sind adds itself: workers of a cluster with [users]({{< relref "/configuration/cluster-config#users-section" >}}) get `SYS_NICE`. slurmstepd binds each task to its CPUs (`task/affinity`) after the task has become the job's user, and setting the CPU affinity of another user's process needs `CAP_SYS_NICE`, which Docker drops by default. Without it, every job of a user other than root fails with `task_g_set_affinity` and "Slurmd could not execve job".

Workers created via `sind create worker` also support these fields:

```bash
sind create worker --cap-add SYS_ADMIN --device /dev/fuse
```

## Default nodes

When the `nodes` section is omitted entirely, sind creates a minimal cluster:

```yaml
nodes:
  - role: controller
  - role: worker
```
