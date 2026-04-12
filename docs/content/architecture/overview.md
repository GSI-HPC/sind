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

sind provides commands for inspection (`get`, `status`), modification (`create/delete worker`), and simulation (`power`), but these are imperative operations, not declarative state management.

This is intentional: sind is a development and testing tool, not a production cluster controller.

## Container-per-node

Each Slurm node runs as a separate Docker container with **systemd as PID 1**. This provides a realistic environment where munge, sshd, and Slurm daemons run as they would on bare metal.

Containers require specific security options for systemd:

- `--security-opt writable-cgroups=true` — allows systemd to manage cgroups
- `--cgroupns=private` — private cgroup namespace
- `--security-opt label=disable` — SELinux compatibility
- `--tmpfs /run:exec,mode=755` — systemd runtime directory
- `--tmpfs /run/lock` — systemd lock files

## Concurrency

Mutating operations acquire a per-realm advisory lock (flock) to serialize concurrent modifications. Read-only operations are unaffected. Different realms operate independently — see [Realms]({{< relref "/configuration/realms" >}}).

## Creation flow

```
PreflightCheck
      │
resolveInfra        DNS IP │ SSH key │ Slurm version
      │
createResources     network ║ (config vol → write) ║ (munge vol → write)
      │
setupNodes          (create + wait + SSH + hostkey) per node
      │
registerMesh ║ enableSlurm
      │
  *Cluster
```

Each node is created, monitored, and probed in a single pipeline — no barrier between node creation and readiness checking. Early-starting nodes begin probing while later nodes are still being created.

Mesh registration (batch DNS + known_hosts) and Slurm enablement run concurrently after all nodes are ready.

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
| munge ready | munge service active |
| slurmctld ready | `scontrol ping` succeeds (controller only) |
| slurmd ready | slurmd service active (worker only) |

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
