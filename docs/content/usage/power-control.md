---
weight: 340
title: "Power Control"
icon: "power_settings_new"
description: "Simulate power events on cluster nodes"
toc: true
---

## Commands

```bash
sind power <action> NODES
```

| Command | Description | Docker operation |
|---------|-------------|-----------------|
| `shutdown` | Graceful shutdown | `docker stop` (SIGTERM, then SIGKILL) |
| `cut` | Hard power off | `docker kill` (immediate SIGKILL) |
| `on` | Power on | `docker start` |
| `reboot` | Graceful reboot | `docker stop` + `docker start` |
| `cycle` | Hard power cycle | `docker kill` + `docker start` |
| `freeze` | Simulate unresponsive node | `docker pause` (cgroup freezer) |
| `unfreeze` | Resume frozen node | `docker unpause` |

## Examples

```bash
# Graceful shutdown of a single worker
sind power shutdown worker-0

# Hard power off multiple workers
sind power cut worker-[0-3]

# Power on after shutdown
sind power on worker-[0-3]

# Graceful reboot
sind power reboot controller

# Hard cycle
sind power cycle worker-[0-1].dev

# Freeze a node (stays "running" but completely unresponsive)
sind power freeze worker-0

# Resume a frozen node
sind power unfreeze worker-0
```

## Freeze and unfreeze

`freeze` uses Docker's cgroup freezer to suspend all processes in the container. The container remains in a "running" state but is completely unresponsive — it won't respond to network requests, SSH connections, or Slurm RPCs.

This is useful for simulating:

- Hung or unreachable nodes
- Network partitions (from the node's perspective)
- Slurm's node health detection and `SlurmdTimeout` behavior

## Node arguments

All power commands accept [nodeset notation](../node-arguments/) for targeting multiple nodes:

```bash
sind power shutdown controller,worker-[0-3]
sind power cycle worker-[0-1].dev,worker-[0-3].default
```
