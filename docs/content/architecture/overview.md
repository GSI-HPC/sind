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

Creating and deleting clusters and workers acquire a per-realm lock to serialize concurrent modifications, and so do `power on`, `reboot` and `cycle`, which start a stopped mesh and rewrite DNS records. Read-only operations and the other `power` commands do not take it. The lock is a file lock (flock) in the user's state directory, which orders that user's commands, and a configuration-only network `<realm>-lock` on the Docker daemon, which orders every client of the daemon: other users, CI jobs that share its socket. A lock that a killed command left on the same host is taken over; one from another host or container is removed by hand. Different realms operate independently — see [Realms]({{< relref "/configuration/realms#advisory-locking" >}}).

## Creation flow

```
┌ PreflightCheck → createResources ───────────────────┐
├ resolveInfra (DNS IP ║ SSH key ║ Slurm version) ─────┤
├ DetectCVMFS (storage.cvmfs only) ────────────────────┼→ setupNodes
└ CheckNsdelegate ─────────────────────────────────────┘
                           │
  registerMesh ║ enableSlurm → createSlurmAccounts ║ createHomes
                           │
                       *Cluster
```

- `createResources` creates the cluster network and connects the SSH relay to it, the config volume and its Slurm configuration (managed clusters only), the munge volume and key (with identity `clientIds`, `slurm.key` on the config volume instead), the data volume (unless the data is a host path), for a backup controller pair the state volume and, with `users`, the home volume, all in parallel.
- `resolveInfra` starts the mesh DNS and SSH relay if they are stopped, and looks up the mesh DNS IP, the SSH public key and, for managed clusters, the Slurm version of the controller's image, while the resources are created.
- `DetectCVMFS` (`storage.cvmfs` only) picks how the nodes [mount CVMFS]({{< relref "/guides/cvmfs" >}}), also while the resources are created: the `cvmfs` Docker volume plugin if one is enabled, otherwise a bind mount of the host's `/cvmfs`, first tried in a throwaway container of the controller's image.
- `CheckNsdelegate` reads `/proc/self/mounts` in a throwaway container of the controller's image, also while the resources are created, and fails the creation, before any node container exists, when the cgroup2 mount lacks `nsdelegate`. `nsdelegate` is an option of the whole cgroup2 hierarchy, which the kernel lists in every cgroup2 mount, so the container shows the option of the kernel the Docker daemon runs on.
- With `--pull`, each distinct node image is pulled once, concurrently, before the helper containers, the version check, the CVMFS check, the nsdelegate check and the nodes run it; none of these containers is created with `--pull always`. A new mesh's DNS and relay containers, which `EnsureMesh` creates before, are created with `--pull always`.
- The helper containers that write the Slurm configuration and the munge key copy each file in with its final mode, then hand the secrets to their owner with `chown`; `docker rm -f` stops them.
- `setupNodes` creates, waits for, and sets up every node: nss_slurm on managed workers with identity `nssSlurm` or `clientIds`, the cluster users and groups where the identity mode puts them, SSH and host keys. The setup is one `docker exec` per node, a script that stops at its first failing step; the error names the step and shows what it wrote.
- `enableSlurm` (managed clusters only) first starts mariadb, the accounting database and slurmdbd on a managed db node, then slurmctld and slurmd, and `sackd` on the submitter with identity `clientIds`.
- `createSlurmAccounts` (`accounts` only) waits until slurmdbd lists the cluster, then creates the Slurm accounts, the users' associations and the coordinators with `sacctmgr -i` on `controller`.
- `createHomes` creates the users' home directories on the shared home volume, once, on `controller` (`users` only).
- If any step fails, `sind create cluster` removes what it created, the mesh only if no other cluster uses it.

Each node is created, monitored, and probed in a single pipeline — no barrier between node creation and readiness checking. Early-starting nodes begin probing while later nodes are still being created.

Mesh registration (batch DNS ║ known_hosts), Slurm enablement and the home directories run concurrently after all nodes are ready. Slurm resolves the nodes' short hostnames on the cluster network, which the nodes join with gateway priority ahead of the mesh (see [Networking]({{< relref "/architecture/networking#cluster-network" >}})), so it does not wait for the mesh DNS records.

## Readiness probes

sind waits for each node to become ready before returning success. Probes are accelerated by two event sources:

- **Docker events** — a single `docker events` stream watches all cluster containers for start/die events
- **Systemd D-Bus monitors** — per-node `busctl monitor --watch-bind=yes` streams watch for unit state changes (e.g., sshd.service becoming active or slurmd.service failing). They run until the command ends, through the waits for the Slurm daemons and the accounts

When an event of the node arrives, its probes re-evaluate immediately instead of waiting for the next poll tick; the events queued by then go with it, so a burst of unit changes during boot costs one probe round. Each node's wait receives only its own container's events. If event sources are unavailable, sind falls back to poll-only mode.

Each round runs a node's checks in order and stops at the first that fails. A check that has passed is not run again in later rounds of the same wait, until an event says that what it checks may have changed: an event of its systemd unit for the sshd, munge and Slurm daemon checks (a failed munge or Slurm daemon unit then ends the wait), any other event of the container for every check. After a full event buffer, or once a monitor has stopped, and in poll-only mode, the passed checks run again. While systemd boots, a round then costs one `docker exec` instead of a `docker inspect` and an exec.

| Check | Description |
|-------|-------------|
| Container running | Docker container in running state |
| systemd ready | `systemctl is-system-running` returns `running` or `degraded` |
| sshd listening | Port 22 accepting connections |
| munge ready | munge service active (not with identity `clientIds`, which masks munge) |
| slurmctld ready | `scontrol ping` reports this controller UP (controllers of managed clusters; each controller of a backup pair is checked for its own host); while it does not, a failed slurmctld unit ends the wait |
| slurmd ready | slurmd service active (managed workers only) |
| sackd ready | sackd service active (the submitter of a managed cluster with identity `clientIds`) |
| slurmdbd ready | slurmdbd service active (managed db nodes). mariadb is started before it with `systemctl enable --now`, which returns once the unit is active |
| cluster registered | `sacctmgr show cluster` on `controller` lists the cluster (`accounts` only, before the accounts are created) |

A unit that has failed does not recover on its own: a failed munge, slurmctld, slurmd, sackd or slurmdbd unit fails `sind create cluster` and `sind create worker` at once, with the tail of the unit's journal. So does a container that exits.

With a managed [db node]({{< relref "/configuration/node-definitions#database-node" >}}), mariadb and slurmdbd are started and slurmdbd must be ready before slurmctld and slurmd are enabled.

## Docker CLI, not SDK

sind interacts with Docker by shelling out to the `docker` CLI rather than using the Docker SDK. This approach, proven by kind, provides:

- Simpler maintenance and fewer dependencies
- Wider compatibility across Docker versions
- No tight coupling to Docker daemon internals

The `docker` package wraps command execution in a thin abstraction layer with proper output handling and error reporting. It runs at most 16 docker commands at once, so a large cluster does not fork hundreds of docker processes while its nodes boot; every node still boots at once, and long-lived streams such as `docker events` do not count.

## Dual use

sind is designed for use as both:

1. **CLI tool** — standalone command-line interface
2. **Go library** — embeddable package for wrapper tools and integrations

The CLI command structure is reflected in the library API, allowing programmatic access to all sind operations.
