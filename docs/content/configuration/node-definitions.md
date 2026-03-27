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
| `cpus` | global + per-node | `2` | CPU limit |
| `memory` | global + per-node | `"2g"` | Memory limit |
| `tmpSize` | global + per-node | `"1g"` | tmpfs size for `/tmp` |
| `count` | worker only | `1` | Number of worker nodes |
| `managed` | worker only | `true` | Start slurmd and add to slurm.conf |

Per-node values override the `defaults` section, which in turn overrides the built-in defaults.

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

## Default nodes

When the `nodes` section is omitted entirely, sind creates a minimal cluster:

```yaml
nodes:
  - role: controller
  - role: worker
```
