---
weight: 230
title: "Realms"
description: "Isolated mesh namespaces for parallel environments"
toc: true
---

## Overview

A **realm** is a namespace that isolates all sind resources — mesh network, DNS, SSH, and cluster resources. Different realms have completely separate infrastructure and cannot see each other.

The default realm is `sind`, which produces resource names like `sind-mesh`, `sind-dns`, `sind-default-net`, etc.

Because names join realm and cluster with `-`, realm `ci` with cluster `42-dev` and realm `ci-42` with cluster `dev` would share the resource names `ci-42-dev-*`. sind refuses to create the second of them, and deleting one leaves the other's network and volumes alone.

## When to use realms

- **Parallel CI jobs** — each job uses a unique realm to avoid resource conflicts
- **Multiple environments** — run separate sets of clusters that don't interfere
- **Testing sind itself** — integration tests use random realms for isolation

Each realm's mesh and each cluster take one Docker network from the daemon's default address pools, which a stock daemon fills at about 30 networks. Hosts that run many realms in parallel need more, smaller pools; see [Limits]({{< relref "/architecture/networking#limits" >}}).

## Setting the realm

Realm is determined by the following precedence (highest first):

{{< ways >}}
{{< cli >}}

| Source | Example |
|--------|---------|
| `--realm` flag | `sind --realm ci-42 create cluster` |
| `SIND_REALM` environment variable | `export SIND_REALM=ci-42` |
| Config file (`sind create cluster` only) | `realm: ci-42` in the YAML config |
| Default | `sind` |

{{< /cli >}}
{{< ui >}}

The realm switch in the top bar selects the realm that the pages show, or all realms. A dialog acts in the realm of its cluster and says so. **New cluster** has a **Realm** field instead, filled in with the realm of the page: type another realm there, or a new name, which makes a new realm. **Connect a container** connects to the realm in its title, and has a button for each realm to switch it, as the container joins the mesh of a realm with a cluster. Its command has `--realm` when the realm is not `sind`, such as `sind --realm ci delete realm`.

{{< /ui >}}
{{< /ways >}}

Only `sind create cluster` reads a config file, so a `realm` set there applies to that command alone. Pass the same realm to the commands that follow, with `--realm` or `SIND_REALM`, or they look in another realm:

```bash
sind create cluster --config dev.yaml       # dev.yaml sets realm: ci-42
sind --realm ci-42 get cluster dev
sind --realm ci-42 delete cluster dev
```

When a command does not find a cluster in its realm, the error names the clusters the realm does hold and the other realms that hold a cluster of that name, for example `cluster "dev" not found in realm "sind" (existing clusters: default; it exists in realm "ci-42")`. `sind get cluster`, `get nodes`, `get node`, `get auth-key`, `enter`, `exec`, `ssh`, `logs`, `power`, `create worker` and `delete worker` all report it this way, and exit `1`.

A realm name must be a single DNS label: lowercase ASCII letters, digits and `-`, 1 to 63 characters, not beginning or ending with `-` (for example `ci-42`, not `CI-42`, `ci_42` or `ci.42`). sind rejects an invalid realm from any source. An invalid `--realm` fails every command with exit status 2, even one that does not use a realm such as `sind version`; `SIND_REALM` is only checked when it is the realm in effect.

## Resource naming

With realm `ci-42`, resources are prefixed accordingly:

| Resource | Default realm (`sind`) | Custom realm (`ci-42`) |
|----------|----------------------|----------------------|
| Mesh network | `sind-mesh` | `ci-42-mesh` |
| DNS container | `sind-dns` | `ci-42-dns` |
| SSH container | `sind-ssh` | `ci-42-ssh` |
| Cluster network | `sind-default-net` | `ci-42-default-net` |
| Container | `sind-default-controller` | `ci-42-default-controller` |
| Realm lock (while a command changes the realm) | `sind-lock` | `ci-42-lock` |

## Advisory locking

Mutating operations (`create cluster`, `delete cluster`, `delete realm`, `create worker`, `delete worker`, `connect container`, `disconnect container`, and `power on`, `reboot` and `cycle`, which start the mesh and rewrite DNS records) acquire a per-realm lock to prevent concurrent modifications. Read-only operations (`get`, `logs`, etc.) are not affected. The lock has two parts:

- A file lock in the invoking user's state directory, which orders that user's commands without asking the Docker daemon:

  ```
  $XDG_STATE_HOME/sind/<realm>/lock    # default: ~/.local/state/sind/<realm>/lock
  ```

- A lock on the Docker daemon, the network `<realm>-lock`, which exists while a command holds the lock. It serializes every sind client of the daemon: several users of a host, CI jobs that share the host's Docker socket, or containers that mount it. The network is configuration-only: it takes no subnet from Docker's address pools and no bridge device, and `sind get networks` does not list it.

If another operation already holds the lock, sind prints `Warning: waiting for another sind command in realm "<realm>" to finish` to stderr and waits until it completes. For a command of another client the warning names it, for example:

{{< ways >}}
{{< cli >}}

```
Warning: waiting for another sind command in realm "sind" to finish: sind create cluster dev (pid 4242 on build-07, since 2026-10-04 10:02:03)
```

{{< /cli >}}
{{< ui >}}

A dialog that changes a realm while another command holds its lock names that command. The Overview names it too. Your command waits until the other one ends, and its run page shows the wait as `realm lock`. To stop the other command, press **Stop** on its run if sind ui started it, or Ctrl-C in its terminal.

{{< /ui >}}
{{< /ways >}}

On a terminal, the [progress display]({{< relref "/usage/progress" >}}) shows the wait as `realm lock`, with how long it has lasted and, for another client's lock, the command it waits for, below the warning, which stays.

The wait has no timeout; Ctrl-C ends it. sind releases the lock when the command ends, also when it fails or is interrupted with Ctrl+C or SIGTERM.

A command killed with SIGKILL leaves the daemon lock behind. When the killed command ran on the same host, in the same container and since the last boot, the next sind command finds that its process is gone, removes the lock and says so (`Warning: removing the realm lock of ..., which no longer runs`). A lock from another host or container is never removed automatically, as its command may still be running; after a minute of waiting, sind prints the command that removes it:

```
Warning: the realm lock is still held by sind create cluster dev (pid 4242 on build-07, since 2026-10-04 10:02:03); if that command no longer runs, remove the lock with: docker network rm sind-lock
```

Locks are per-realm — operations in different realms run concurrently without contention, making realm-based CI isolation safe for parallel jobs. Clients that share a realm are safe from each other, but not isolated: `sind delete cluster --all` deletes the other client's clusters too, and cluster names must differ. Give independent jobs a realm each.

Go programs that use sind as a library take the same lock with `state.LockRealm` from `github.com/GSI-HPC/sind/pkg/state` around `cluster.Create`, `cluster.Delete`, `cluster.DeleteAll`, `cluster.WorkerAdd`, `cluster.WorkerRemove`, `cluster.PowerOn`, `cluster.PowerReboot` and `cluster.PowerCycle`, which do not lock themselves. They pass their Docker client as `LockOptions.Client`; without it, only the file lock is taken, which does not serialize other clients of the daemon.

## Example

```bash
# Create two isolated environments
sind --realm dev create cluster app
sind --realm staging create cluster app

# Each has its own mesh, DNS, and clusters
sind --realm dev get clusters
sind --realm staging get clusters

# Tear down independently
sind --realm dev delete cluster app
sind --realm staging delete cluster app
```
