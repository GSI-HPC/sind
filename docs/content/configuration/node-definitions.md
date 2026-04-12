---
weight: 220
title: "Node Definitions"
icon: "dns"
description: "Node roles, shorthand syntax, and managed vs unmanaged workers"
toc: true
---

## Node roles

| Role | Count | Required | Slurm daemons | Description |
|------|-------|----------|---------------|-------------|
| `controller` | exactly 1 | yes | slurmctld | Cluster controller |
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
| `managed` | worker only | `true` | Start slurmd and add to slurm.conf |
| `backupController` | controller only | `false` | Also create an idle `controller-backup` container (see below) |
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

## Backup controller

Setting `backupController: true` on the controller node spec spawns a second
controller container named `controller-backup` alongside the primary
`controller`. The backup uses the same image, resources, volumes,
capabilities, devices, and security options as the primary — both containers
share the cluster's config, munge, and data volumes — but sind does **not**
start `slurmctld` on it. It comes up idle, ready for manual debug runs or
active/passive experiments.

```yaml
nodes:
  - role: controller
    backupController: true
  - role: worker
    count: 2
```

Typical uses:

- Running `slurmctld -Dvvvvvv` by hand against the same `/etc/slurm` layout
  to investigate controller behavior.
- Trying out Slurm's active/passive `SlurmctldHost` failover configuration
  without having to rebuild the cluster.

The backup controller is addressable via DNS at
`controller-backup.<cluster>.<realm>.sind` and can be entered with
`sind enter controller-backup`. Worker discovery, `slurm.conf` generation,
and the `sind create worker` / `sind delete worker` flows all continue to
reference the primary `controller` container — the backup is invisible to
them.

## Capabilities and devices

sind's default security posture avoids extra capabilities and device access. When specific use cases require them (e.g. testing CVMFS provisioning or FUSE-based filesystems), you can grant targeted privileges per node.

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
