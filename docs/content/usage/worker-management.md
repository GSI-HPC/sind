---
weight: 330
title: "Worker Management"
icon: "group_add"
description: "Dynamically adding and removing worker nodes"
toc: true
---

{{< video "worker-management" >}}

## Add workers

```bash
sind create worker [CLUSTER] [FLAGS]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--count` | `1` | Number of nodes to add |
| `--image` | the newest worker's image, else the controller's | Container image |
| `--cpus` | the newest worker's, else `1` | CPU limit per node |
| `--memory` | the newest worker's, else `512m` | Memory limit, without swap; it covers the node's services and `/tmp` files too |
| `--tmp-size` | the newest worker's, else `256m` | `/tmp` tmpfs size, part of `--memory` |
| `--unmanaged` | `false` | Don't start slurmd, don't add to slurm.conf (implied on unmanaged clusters) |
| `--pull` | `false` | Pull the `--image` once before creating containers; needs `--image` |
| `--wait` | `5m` | How long to wait for the new workers to become ready, counted from when their containers have started; `0` for no limit |
| `--cap-add` | the newest worker's, else none | Add Linux capability (repeatable; e.g. `SYS_ADMIN`); `--cap-add=` for none |
| `--cap-drop` | the newest worker's, else none | Drop Linux capability (repeatable); `--cap-drop=` for none |
| `--device` | the newest worker's, else none | Expose host device (repeatable; e.g. `/dev/fuse`); `--device=` for none |
| `--security-opt` | the newest worker's, else none | Security option (repeatable); `--security-opt=` for none |

### Defaults from the newest worker

New workers look like the cluster's newest worker: the one with the highest index among the workers managed like the new ones, or among all workers if none is. sind reads its image, CPU and memory limits, `/tmp` size, capabilities, devices and security options from Docker, so `sind create worker` on a cluster created with `cpus: 2`, `memory: 1g` or `capAdd: [SYS_ADMIN]` and `devices: [/dev/fuse]` adds workers with the same settings, and the same `CPUs` and `RealMemory` in `sind-nodes.conf`. Each flag you give replaces the inherited value; `--cap-add`, `--cap-drop`, `--device` and `--security-opt` replace the whole inherited list. Give one empty to add workers without any, e.g. `sind create worker --device=` on a cluster whose workers have `/dev/fuse`.

Not inherited:

- the security options that sind gives every node, and the `SYS_NICE` capability where sind gave it to a managed worker for `task/affinity` (see [Capabilities and devices]({{< relref "/configuration/node-definitions#capabilities-and-devices" >}})), as sind decides them for each new worker. A `SYS_NICE` you asked for yourself, with sind's default `TaskPlugin` or on unmanaged workers, is inherited;
- a seccomp profile, which Docker reports by content rather than by file: pass it again with `--security-opt seccomp=FILE`.

A cluster without workers gets the controller's image and 1 CPU, `512m` memory and a `256m` `/tmp`.

### Image and Slurm version

Without `--image`, new workers run the image the newest worker (or the controller) runs, by its ID: the tag it was created from may since point to another image, even another Slurm release. `--pull` therefore needs `--image`.

With `--image`, sind pulls the image once if the daemon does not have it (with `--pull` in any case), runs `slurmctld -V` in it, and refuses managed workers whose Slurm version is not the cluster's: slurmd must not be newer than slurmctld. To add workers from a moved tag, name the cluster's release, for example `--image ghcr.io/gsi-hpc/sind-node:25.11.8`.

Before it creates a worker container, sind checks that the Docker host still mounts cgroup2 with `nsdelegate`, in a throwaway container of the controller's image, as [`sind create cluster`]({{< relref "/usage/cluster-lifecycle#preflight-checks" >}}) does, and fails with the commands that enable it otherwise.

### Checks

`--count` must be at least 1 and `--cpus` must not be negative. `--cap-add` and `--cap-drop` take the capability names the cluster config's `capAdd` and `capDrop` accept, `--device` needs an absolute host path, as `devices` does, `--security-opt` an option Docker knows, as `securityOpt` does, and `--memory` and `--tmp-size` take the sizes `memory` and `tmpSize` take (see [Defaults section]({{< relref "/configuration/cluster-config#defaults-section" >}})). sind rejects these, and `--pull` without `--image`, with exit status 2 before it creates any container. With `-v`, it logs the extra privileges of the new nodes, as `sind create cluster` does.

Workers that would not fit on the realm's mesh or the cluster network, Docker bridge networks of at most 1,023 containers each, fail with exit status 1 before sind creates any of them (see [Limits]({{< relref "/architecture/networking#limits" >}})).

### Examples

```bash
# 1 managed worker like the newest worker
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

If a step fails, or you interrupt the command, sind removes the new containers again and writes back `sind-nodes.conf` as it found it, so you can simply retry. If that cleanup fails too, the error says so after the original one.

On an [unmanaged cluster]({{< relref "/guides/unmanaged-cluster" >}}), every new worker is unmanaged, with or without `--unmanaged`.

If a new worker is not ready within `--wait`, or its munge or slurmd unit fails, `sind create worker` fails with exit status 1, names the worker and the check that failed, and removes the workers it created, as `sind create cluster` does (see [Cluster Lifecycle]({{< relref "/usage/cluster-lifecycle#what-happens-during-creation" >}})).

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
