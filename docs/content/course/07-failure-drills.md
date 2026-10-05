---
weight: 77
title: "07 · Failure drills"
description: "Power off, freeze, scale and fail over, and see how Slurm notices"
---

{{< video "course-07-failure-drills" >}}

You run a Slurm site, or you build a tool that submits jobs to Slurm, and you need to know what your jobs and tools do when a node dies, hangs or joins, or the controller fails. This episode rehearses each of these failures on throwaway clusters, with nothing but sind and Docker, and shows how Slurm notices.

## In this episode

- Add and remove workers on a running cluster with [Worker Management]({{< relref "/usage/worker-management" >}}).
- Freeze, cut and power on workers with [Power Control]({{< relref "/usage/power-control" >}}), and how slurmctld marks them `DOWN` ([Node failure detection]({{< relref "/usage/power-control#node-failure-detection" >}})).
- Lower `SlurmdTimeout` to keep drills and [CI node failure tests]({{< relref "/guides/ci-cd#node-failure-tests" >}}) short.
- Freeze the primary of a controller pair and let the backup take over with [Controller Failover]({{< relref "/guides/controller-failover" >}}).

## Commands

The commands and output from the video, in order. First, a cluster named `default` with one controller and one worker, and three more workers:

```bash
sind create cluster
sind create worker --count 3
```

A worker hangs, and comes back:

```bash
sind power freeze worker-0
sind power unfreeze worker-0
```

All four workers lose power, come back, and one leaves for good:

```bash
sind power cut worker-[0-3]
sind power on worker-[0-3]
sind delete worker worker-2
```

With Slurm's default `SlurmdTimeout=300`, slurmctld marks a worker that stopped responding `DOWN` after about five minutes. For drills, lower it in the cluster config:

```yaml
slurm:
  main: |
    SlurmdTimeout=30
```

A second cluster, `dev`, with a backup controller:

```yaml
kind: Cluster
name: dev
nodes:
  - role: controller
    backupController: true
  - role: worker
    count: 2
```

```bash
sind create cluster --config ha.yaml
sind get cluster dev
```

```text
NODES
NAME                    ROLE        HA         IP            STATUS    SERVICES
controller.dev          controller  primary*   172.19.0.2    running   munge ✓ slurmctld ✓ sshd ✓
controller-backup.dev   controller  backup     172.19.0.3    running   munge ✓ slurmctld ✓ sshd ✓
worker-0.dev            worker                 172.19.0.4    running   munge ✓ slurmd ✓ sshd ✓
```

The primary hangs, the backup takes over, and the primary gets control back:

```bash
sind power freeze controller.dev
sind power unfreeze controller.dev
sind ssh controller.dev -- systemctl restart slurmctld
sind exec dev -- scontrol ping
```

```text
Slurmctld(primary) at controller is UP
Slurmctld(backup) at controller-backup is UP
```

## Refresher: Node states

Idle, allocated, `idle*` and `DOWN`, and how a node that stops answering goes down and comes back: [the full refresher]({{< relref "refreshers#node-states" >}}).

## Go deeper

- [Power Control]({{< relref "/usage/power-control" >}}): every power action and the Docker operation behind it, with a clip.
- [Worker Management]({{< relref "/usage/worker-management" >}}): flags, defaults from the newest worker, and what sind changes in Slurm, with a clip.
- [Controller Failover]({{< relref "/guides/controller-failover" >}}): graceful takeover and every outage scenario, with a clip.
- [CI/CD]({{< relref "/guides/ci-cd" >}}): node failure tests in a pipeline.
- [Slurm Configuration]({{< relref "/architecture/slurm-config" >}}): `ReturnToService=2` and the other parameters sind sets.
