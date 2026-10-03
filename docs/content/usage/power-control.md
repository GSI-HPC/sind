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
| `shutdown` | Graceful shutdown | `docker stop` (the image's stop signal, `SIGRTMIN+3` for sind-node, then SIGKILL after 10 s) |
| `cut` | Hard power off | `docker kill` (immediate SIGKILL) |
| `on` | Power on | `docker start` |
| `reboot` | Graceful reboot | `docker stop` + `docker start` |
| `cycle` | Hard power cycle | `docker kill` + `docker start` |
| `freeze` | Simulate unresponsive node | `docker pause` (cgroup freezer) |
| `unfreeze` | Resume frozen node | `docker unpause` |

`docker stop` sends the image's stop signal. The sind-node images set `SIGRTMIN+3`, which makes systemd shut the node down cleanly. A custom image needs the same [stop signal]({{< relref "/container-images/building-images#container-settings" >}}): without it the node gets SIGTERM, which systemd does not treat as a shutdown request, and Docker kills it after 10 seconds.

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

## Node failure detection

slurmctld learns that a frozen, cut or shut down worker is gone only when the worker stops answering its pings, as slurmd does not sign off. sind keeps Slurm's default `SlurmdTimeout=300`: the node shows as `idle*` once a ping fails and turns `DOWN` about five minutes after it stopped responding. For failure tests, lower the timeout in the [`main` section]({{< relref "/configuration/cluster-config#slurm-section" >}}):

```yaml
slurm:
  main: |
    SlurmdTimeout=30
```

slurmctld pings about every third of the timeout, so a worker is then `DOWN` within about 30 to 40 seconds. A short timeout also marks a slow but healthy node `DOWN`, which kills its jobs, e.g. on an overloaded CI runner. With `ReturnToService=2`, which sind sets, the node returns to service once its slurmd registers again, e.g. after `sind power unfreeze` or `sind power on`.

## Node arguments

All power commands accept [nodeset notation](../node-arguments/) for targeting multiple nodes:

```bash
sind power shutdown controller,worker-[0-3]
sind power cycle worker-[0-1].dev,worker-[0-3].default
```
