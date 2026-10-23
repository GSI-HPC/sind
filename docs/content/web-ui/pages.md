---
weight: 341
title: "Pages"
description: "What each page of sind ui shows"
toc: true
---

The rail on the left lists the pages; on a phone, the bar at the bottom does, with the less used pages under **More**. The pages update by themselves. A cluster page refreshes its state while it is open, and the Overview and the lists update when Docker reports a change.

## Overview

The things that need you come first, then the realms, a card for each cluster and the recent activity. Each realm's row has **Clusters** and **Delete realm…**. An empty realm says so and offers **New cluster…**.

The head names the problems, such as `1 node is stopped · 1 run failed`, and links to **Needs you**. A node needs a look when its container does not run, or when a service on it is down. The Overview, the realm table, the lists and the rail count by this one rule. sind ui knows that a service is down once it has probed the cluster: every 30 seconds while any page is open, and every 10 seconds while a page of the cluster is open. A service that is down does not count while a command works on its cluster, such as one that adds workers or reboots the node: the command may be starting it. No node of a cluster counts while `sind create cluster` makes it.

{{< uishot "overview" >}}

## Clusters

Every cluster of the realm. sind keeps no list of clusters. It finds them by their Docker labels.

{{< uishot "clusters" >}}

## Nodes

Every node of the realm. A node is named as you type it in commands, such as `worker-0.dev`.

{{< uishot "nodes" >}}

## Cluster

The state of one cluster, in six tabs.

**Summary** starts with **Ready for jobs**: it says whether the cluster can run a job now, and checks the controller, the workers, accounting and where your files are. A check that fails names the node and links to its logs. While `sind create cluster` makes the cluster, it says `not yet` and offers no fix. Beside the checks are the first two commands of a job, `sind enter` and `sbatch`, with **Copy**. Below are the nodes as tiles, with a legend of what each tile means, a matrix of nodes and services, such as munge, sshd and slurmctld, and the network checks. Each tile and each cell of the matrix has its state in words for screen readers and as a tooltip, such as `worker-4.dev, running, slurmd down`. The **Nodes** tab keeps the number of nodes and adds how many have a problem.

{{< uishot "cluster" >}}

**Nodes** lets you select nodes. The selection appears as a node set, such as `worker-[2-4]`. Power and delete act on the selection. When you select only workers, **Drain…** and **Resume…** appear, and the delete button names the workers, such as **Delete worker-5…** or **Delete 3 workers…**. The **Slurm** column shows the state that `sinfo` reports for each worker, such as `idle`, `allocated` or `drained`, and the reason.

{{< uishot "cluster-nodes-selected" >}}

**Slurm** shows what Slurm says: the partitions, the state, reason, CPUs and memory of each worker, and the `slurm.conf` and `sind-nodes.conf` that sind wrote, each with **Copy**. **View slurm.conf** on the Summary leads here. sind ui runs `sinfo` on the controller while a page of the cluster is open, and gives it 10 seconds. When slurmctld does not answer, the tab says why and still shows the files, which the controller reads without slurmctld.

{{< uishot "cluster-slurm" >}}

**Logs** shows the container log of a node, or its journal: one unit, or all units. **Start with** sets how many lines it shows first: 200, 1,000 or 5,000. It keeps the last 10,000. Use **Pause** to stop the stream and **Follow** to resume it.

Type in **Filter** to show only the lines with that text, or tick **Errors and warnings only**. The filter hides lines and keeps them. **Copy lines** and **Download** take the lines that show. The address keeps every choice, so a reload or a shared link shows the same log.

{{< uishot "cluster-logs" >}}

**Network and storage** lists the networks of the cluster, and its volumes and mounts with their kind, a Docker volume or a host directory, and their mode: the controllers write `/etc/slurm` and the other nodes only read it, `/etc/munge` and `/cvmfs` are read-only, and the others read-write. **Volume** says whether Docker has the volume; a host directory is not checked. It shows the address and DNS name of each node, the DNS zone and how nodes are named in it, the DNS server and SSH relay of the realm, and where the munge key is. Values you may paste elsewhere have **Copy**. The key itself is never shown.

{{< uishot "cluster-network" >}}

**Connect** shows the commands to open a shell on a node, to run a command, and to use `ssh`. Use **Copy** to copy one.

{{< uishot "cluster-connect" >}}

## Node

The services, events and power state of one node, what Slurm says about it (state, reason, CPUs, memory and partitions), and the commands for it. A worker's page also has **Drain…**, **Resume…** and **Delete worker…**. **Logs** opens the log of the node: the journal of its Slurm service while it runs, otherwise the container output. Each service has a **Logs** link to its own journal.

{{< uishot "node" >}}

## Mesh and DNS

The mesh network, the DNS server and its records, and the containers that are connected to the realm.

{{< uishot "mesh" >}}

## Monitoring

The state and addresses of Grafana, Prometheus, Loki and Alloy, if the realm runs them. Use **read every** to set how often sind ui reads them. The change lasts until sind ui stops.

{{< uishot "monitoring" >}}

## Topology

A diagram from the host, through the mesh services, to the cluster networks and the nodes.

{{< uishot "topology" >}}

## Doctor

The host checks of `sind doctor`, each as ok, warning or failed, with the fix for each.

{{< uishot "doctor" >}}

## Activity

The runs of sind commands that change something, from this UI or from a terminal. A run opens its run page (see [Runs and Progress]({{< relref "/web-ui/runs" >}})).

{{< uishot "activity" >}}

## Help

Every how-to of the help pane, with links to all words and to the keys. The rail has no entry for it: open it from **All how-tos** in the help pane, or at the address `/help`. An entry of the help pane has an address of its own, such as `/help?topic=word/realm`, and the search of the page, such as `/help?q=sbatch`, works without script (see [Find things and get help]({{< relref "/web-ui#find-things-and-get-help" >}})).
