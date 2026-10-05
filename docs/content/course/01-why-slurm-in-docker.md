---
weight: 71
title: "01 · Why Slurm in Docker?"
description: "Testing on a real cluster is slow and risky; here is a cluster in seconds instead"
---

{{< video "course-01-why-slurm-in-docker" >}}

You have a new job script, a configuration change or a CI pipeline to test, and a production cluster is the wrong place to try it. This episode shows what sind does instead: one container per node, a whole Slurm cluster from one command, and the trade-off behind it.

## In this episode

- Who needs a test cluster, and why the usual options cost time and effort: [Introduction]({{< relref "/introduction#why-sind" >}})
- One container per node, with systemd as PID 1: [Design Overview]({{< relref "/architecture/overview#container-per-node" >}})
- A cluster from one command, what that command does, its node containers, and the cluster deleted again: [Quickstart]({{< relref "/getting-started/quickstart" >}})
- The trade-off: some flexibility for ease of use and speed, for development and testing rather than production: [Design Overview]({{< relref "/architecture/overview#one-shot-not-reconciling" >}})

## Commands

The episode creates a cluster, lists its nodes and deletes the cluster again, as in the [Quickstart]({{< relref "/getting-started/quickstart" >}}):

```bash
sind create cluster
```

```bash
sind get nodes
```

```text
CONTAINER                CLUSTER   ROLE         FQDN                            IP           STATUS
sind-default-controller  default   controller   controller.default.sind.sind    172.19.0.2   running
sind-default-worker-0    default   worker       worker-0.default.sind.sind      172.19.0.3   running
```

```bash
sind delete cluster default
```

## Refresher: Slurm in a nutshell

The controller, the compute nodes, the queue and munge, in one picture. The full refresher, [Slurm in 90 seconds]({{< relref "refreshers#slurm-in-90-seconds" >}}), also covers partitions, accounting and how sind's node roles map onto them.

## Go deeper

- [Introduction]({{< relref "/introduction" >}}): what sind is and the features behind the pitch
- [Design Overview]({{< relref "/architecture/overview" >}}): the operational model, container-per-node and the creation flow
- [Node Definitions]({{< relref "/configuration/node-definitions#node-roles" >}}): the controller, db, submitter and worker roles
- [Quickstart]({{< relref "/getting-started/quickstart" >}}) and [Installation]({{< relref "/getting-started/installation" >}}): both open with a short clip
