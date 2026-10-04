---
weight: 81
title: "Refreshers"
description: "About a minute of background each, on Slurm, Docker and the tools around them"
toc: true
---

Refreshers explain the background a sind feature builds on, in about a minute each. A shortened version of each one appears in the course episode named under it.

## Slurm in 90 seconds

{{< video "refresher-slurm" >}}

The controller, the workers and the queue: what slurmctld and slurmd do, and how a job gets from sbatch to a node. Shortened in [01 · Why Slurm in Docker?]({{< relref "01-why-slurm-in-docker" >}}).

## Docker networks and DNS

{{< video "refresher-docker-networks" >}}

How containers on a bridge network reach each other, and how names turn into addresses. Shortened in [03 · Inside a sind cluster]({{< relref "03-inside-a-cluster" >}}).

## MPI ranks and srun

{{< video "refresher-mpi" >}}

What an MPI rank is, and how srun starts one per task across the nodes. Shortened in [04 · Test your job scripts before the queue]({{< relref "04-job-scripts" >}}).

## Accounting and associations

{{< video "refresher-accounting" >}}

How slurmdbd records jobs, and how users, accounts and limits link up as associations. Shortened in [05 · Model your site]({{< relref "05-model-your-site" >}}).

## slurm.conf on one screen

{{< video "refresher-slurm-conf" >}}

The handful of lines that define a Slurm cluster: the controller, the nodes and the partitions. Shortened in [06 · Rehearse a change]({{< relref "06-rehearse-a-change" >}}).

## Node states

{{< video "refresher-node-states" >}}

Idle, allocated, down and drained: what each state means and how a node gets into it. Shortened in [07 · Failure drills]({{< relref "07-failure-drills" >}}).

## systemd as PID 1

{{< video "refresher-systemd" >}}

Why sind's nodes run systemd as their first process, and what that gives you over a container that runs one program. Shortened in [09 · Bring your own provisioning]({{< relref "09-own-provisioning" >}}).

## What MCP is

{{< video "refresher-mcp" >}}

The Model Context Protocol: how an AI assistant discovers and calls tools such as sind's. Shortened in [10 · Let your assistant drive]({{< relref "10-assistant" >}}).
