---
weight: 330
title: "Worker Management"
icon: "group_add"
description: "Dynamically adding and removing worker nodes"
toc: true
---

## Add workers

```bash
sind create worker [CLUSTER] [FLAGS]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--count` | `1` | Number of nodes to add |
| `--image` | cluster default | Container image |
| `--cpus` | cluster default (1) | CPU limit per node |
| `--memory` | cluster default (512m) | Memory limit |
| `--tmp-size` | `256m` | `/tmp` tmpfs size |
| `--unmanaged` | `false` | Don't start slurmd, don't add to slurm.conf |
| `--pull` | `false` | Pull images before creating containers |
| `--cap-add` | none | Add Linux capability (repeatable; e.g. `SYS_ADMIN`) |
| `--cap-drop` | none | Drop Linux capability (repeatable) |
| `--device` | none | Expose host device (repeatable; e.g. `/dev/fuse`) |
| `--security-opt` | none | Security option (repeatable) |

### Examples

```bash
# 1 managed worker with cluster defaults
sind create worker

# 3 managed workers
sind create worker --count 3

# Workers in a named cluster
sind create worker dev --count 2

# With resource limits
sind create worker --cpus 2 --memory 1g

# Unmanaged workers (slurmd not started)
sind create worker --count 2 --unmanaged
```

### Managed worker workflow

For managed workers (the default), sind:

1. Creates the worker container(s)
2. Appends node definitions to `sind-nodes.conf`
3. Reconfigures slurmctld (`scontrol reconfigure`)
4. Starts slurmd on the new node(s)

This requires `sind-nodes.conf` to exist in `/etc/slurm`. If you replaced the generated Slurm configuration, use `--unmanaged` instead.

## Remove workers

```bash
sind delete worker NODES
```

For managed workers, sind removes them from `sind-nodes.conf` and reconfigures slurmctld before deleting the container. Works with both managed and unmanaged nodes.

```bash
# Remove a single worker
sind delete worker worker-2

# Remove multiple workers
sind delete worker worker-[2-4]

# Remove workers from a named cluster
sind delete worker worker-[0-1].dev
```

See [Node Arguments](../node-arguments/) for the full nodeset expansion syntax.
