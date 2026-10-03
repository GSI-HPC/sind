---
weight: 410
title: "Design Overview"
icon: "info"
description: "Operational model and design philosophy"
toc: true
---

## One-shot, not reconciling

While the cluster configuration resembles a Kubernetes manifest, sind is **not** a reconciling controller. The configuration is a one-shot input:

- `sind create cluster` interprets the manifest once to generate the cluster
- sind does not watch or reconcile cluster state
- sind does not automatically repair drift or failures

sind provides commands for inspection (`get`), modification (`create/delete worker`), and simulation (`power`), but these are imperative operations, not declarative state management.

This is intentional: sind is a development and testing tool, not a production cluster controller.

## Container-per-node

Each Slurm node runs as a separate Docker container with **systemd as PID 1**. This provides a realistic environment where munge, sshd, and Slurm daemons run as they would on bare metal.

Containers require specific security options for systemd:

- `--security-opt writable-cgroups=true` — allows systemd to manage cgroups
- `--cgroupns=private` — private cgroup namespace
- `--security-opt label=disable` — lets systemd manage its cgroups, and the nodes use host bind mounts, on Docker daemons with SELinux labelling enabled, without host policy changes. The nodes then run unconfined (`spc_t` instead of `container_t`), so SELinux does not separate them from the host
- `--tmpfs /run:exec,mode=755,size=64m` — systemd runtime directory, sized so that a volatile journal cannot grow into the node's memory
- `--tmpfs /run/lock` — systemd lock files
- `--entrypoint /bin/sh` with a short script — PID 1 moves itself into `init.scope`, enables every available cgroup controller in the root cgroup's `cgroup.subtree_control`, then execs `/sbin/init`

`docker exec` puts its process in the container's root cgroup unless that cgroup has controllers enabled, and with a process there systemd can no longer enable them (cgroup v2's no internal processes rule). Enabling them before systemd starts keeps a `docker exec` of sind's from racing systemd's boot, so `Delegate=yes` daemons such as slurmd always get the memory and cpu controllers.

## Concurrency

Creating and deleting clusters and workers acquire a per-realm advisory lock (flock) to serialize concurrent modifications. Read-only operations and `power` commands do not take it. Different realms operate independently — see [Realms]({{< relref "/configuration/realms" >}}).

## Creation flow

```
┌ PreflightCheck → createResources → connect SSH relay ┐
├ resolveInfra (DNS IP ║ SSH key ║ Slurm version) ─────┼→ setupNodes
└ DetectCVMFS (storage.cvmfs only) ────────────────────┘
                           │
  registerMesh ║ enableSlurm → createSlurmAccounts ║ createHomes
                           │
                       *Cluster
```

- `createResources` creates the cluster network, the config volume and its Slurm configuration (managed clusters only), the munge volume and key (with identity `clientIds`, `slurm.key` on the config volume instead), the data volume (unless the data is a host path), for a backup controller pair the state volume and, with `users`, the home volume, all in parallel.
- `resolveInfra` looks up the mesh DNS IP, the SSH public key and, for managed clusters, the Slurm version of the controller's image, while the resources are created.
- `DetectCVMFS` (`storage.cvmfs` only) picks how the nodes [mount CVMFS]({{< relref "/guides/cvmfs" >}}), also while the resources are created: the `cvmfs` Docker volume plugin if one is enabled, otherwise a bind mount of the host's `/cvmfs`, first tried in a throwaway container of the controller's image.
- `setupNodes` creates, waits for, and sets up every node: nss_slurm on managed workers with identity `nssSlurm` or `clientIds`, the cluster users and groups where the identity mode puts them, SSH and host keys.
- `enableSlurm` (managed clusters only) first starts mariadb, the accounting database and slurmdbd on a managed db node, then slurmctld and slurmd, and `sackd` on the submitter with identity `clientIds`.
- `createSlurmAccounts` (`accounts` only) waits until slurmdbd lists the cluster, then creates the Slurm accounts, the users' associations and the coordinators with `sacctmgr -i` on `controller`.
- `createHomes` creates the users' home directories on the shared home volume, once, on `controller` (`users` only).
- If any step fails, `sind create cluster` removes what it created.

Each node is created, monitored, and probed in a single pipeline — no barrier between node creation and readiness checking. Early-starting nodes begin probing while later nodes are still being created.

Mesh registration (batch DNS + known_hosts), Slurm enablement and the home directories run concurrently after all nodes are ready.

## Readiness probes

sind waits for each node to become ready before returning success. Probes are accelerated by two event sources:

- **Docker events** — a single `docker events` stream watches all cluster containers for start/die events
- **Systemd D-Bus monitors** — per-node `busctl monitor --watch-bind=yes` streams watch for unit state changes (e.g., sshd.service becoming active)

When an event arrives, probes re-evaluate immediately instead of waiting for the next poll tick. If event sources are unavailable, sind falls back to poll-only mode.

| Check | Description |
|-------|-------------|
| Container running | Docker container in running state |
| systemd ready | `systemctl is-system-running` returns `running` or `degraded` |
| sshd listening | Port 22 accepting connections |
| munge ready | munge service active (not with identity `clientIds`, which masks munge) |
| slurmctld ready | `scontrol ping` reports this controller UP (controllers of managed clusters; each controller of a backup pair is checked for its own host) |
| slurmd ready | slurmd service active (managed workers only) |
| sackd ready | sackd service active (the submitter of a managed cluster with identity `clientIds`) |
| slurmdbd ready | slurmdbd service active (managed db nodes); a failed unit fails `sind create cluster` at once with the unit's journal tail. mariadb is started before it with `systemctl enable --now`, which returns once the unit is active |
| cluster registered | `sacctmgr show cluster` on `controller` lists the cluster (`accounts` only, before the accounts are created) |

With a managed [db node]({{< relref "/configuration/node-definitions#database-node" >}}), mariadb and slurmdbd are started and slurmdbd must be ready before slurmctld and slurmd are enabled.

## Docker CLI, not SDK

sind interacts with Docker by shelling out to the `docker` CLI rather than using the Docker SDK. This approach, proven by kind, provides:

- Simpler maintenance and fewer dependencies
- Wider compatibility across Docker versions
- No tight coupling to Docker daemon internals

The `docker` package wraps command execution in a thin abstraction layer with proper output handling and error reporting.

## Dual use

sind is designed for use as both:

1. **CLI tool** — standalone command-line interface
2. **Go library** — embeddable package for wrapper tools and integrations

The CLI command structure is reflected in the library API, allowing programmatic access to all sind operations.
