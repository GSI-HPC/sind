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
| `--image` | the controller's image | Container image |
| `--cpus` | `1` | CPU limit per node |
| `--memory` | `512m` | Memory limit, without swap; it covers the node's services and `/tmp` files too |
| `--tmp-size` | `256m` | `/tmp` tmpfs size, part of `--memory` |
| `--unmanaged` | `false` | Don't start slurmd, don't add to slurm.conf (implied on unmanaged clusters) |
| `--pull` | `false` | Pull images before creating containers |
| `--cap-add` | none | Add Linux capability (repeatable; e.g. `SYS_ADMIN`) |
| `--cap-drop` | none | Drop Linux capability (repeatable) |
| `--device` | none | Expose host device (repeatable; e.g. `/dev/fuse`) |
| `--security-opt` | none | Security option (repeatable) |

### Checks

`--count` must be at least 1 and `--cpus` must not be negative. `--cap-add` and `--cap-drop` take the capability names the cluster config's `capAdd` and `capDrop` accept, `--device` needs an absolute host path, as `devices` does, and `--security-opt` an option Docker knows, as `securityOpt` does. sind rejects these with exit status 2 before it creates any container. With `-v`, it logs the extra privileges of the new nodes, as `sind create cluster` does.

### Examples

```bash
# 1 managed worker with default resources
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
2. Adds node definitions to `sind-nodes.conf`
3. Reconfigures slurmctld (`scontrol reconfigure`)
4. Starts slurmd on the new node(s)

This requires a running controller and `sind-nodes.conf` in `/etc/slurm`. If you replaced the generated Slurm configuration, use `--unmanaged` instead. If the controller is stopped or frozen, sind fails with an error that says so: run `sind power on` or `sind power unfreeze` on it first.

If a step fails, or you interrupt the command, sind removes the new containers and their `sind-nodes.conf` definitions again, so you can simply retry.

On an [unmanaged cluster]({{< relref "/guides/unmanaged-cluster" >}}), every new worker is unmanaged, with or without `--unmanaged`.

## Remove workers

```bash
sind delete worker NODES
```

For managed workers, sind removes them from `sind-nodes.conf` and reconfigures slurmctld before deleting the container. Works with both managed and unmanaged nodes. On an unmanaged cluster, sind never edits the Slurm configuration.

Deleting a managed worker needs a running controller: with the controller stopped or frozen, `sind delete worker` fails and removes nothing, as the node would otherwise stay in Slurm's configuration without a container. Unmanaged workers can be deleted at any time.

```bash
# Remove a single worker
sind delete worker worker-2

# Remove multiple workers
sind delete worker worker-[2-4]

# Remove workers from a named cluster
sind delete worker worker-[0-1].dev
```

See [Node Arguments](../node-arguments/) for the full nodeset expansion syntax.
