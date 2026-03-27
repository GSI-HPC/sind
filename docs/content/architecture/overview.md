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

## Creation flow

```
PreflightCheck
      │
resolveInfra        DNS IP │ SSH key │ Slurm version
      │
createResources     network │ volumes → config │ munge
      │
createAllNodes      node₁ │ node₂ │ ... │ nodeₙ
      │
setupNodes          (wait + SSH + hostkey) per node
      │
registerMesh        DNS records + known_hosts
      │
enableSlurm         (enable + probe) per eligible node
      │
  *Cluster
```

Nodes start in parallel because the global infrastructure (DNS, SSH) is already available. Slurm daemons handle transient connection failures during bootstrap.

## Readiness probes

sind waits for each node to become ready before returning success:

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
