---
weight: 341
title: "Pages"
description: "What each page of sind ui shows"
toc: true
---

The rail on the left lists the pages; on a phone, the bar at the bottom does, with the less used pages under **More**. The pages update by themselves. A cluster page refreshes its state while it is open, and the Overview and the lists update when Docker reports a change.

## Overview

A card for each cluster, the things that need you, and the recent activity. An empty realm says so and offers **New cluster…**.

{{< uishot "overview" >}}

## Clusters

Every cluster of the realm. sind keeps no list of clusters. It finds them by their Docker labels.

{{< uishot "clusters" >}}

## Nodes

Every node of the realm. A node is named as you type it in commands, such as `worker-0.dev`.

{{< uishot "nodes" >}}

## Cluster

The state of one cluster, in five tabs.

**Summary** shows the nodes by role and a matrix of nodes and services, such as munge, sshd and slurmctld, and the network checks.

{{< uishot "cluster" >}}

**Nodes** lets you select nodes. The selection appears as a node set, such as `worker-[2-4]`. Power and delete act on the selection. When you select only workers, **Drain…** and **Resume…** appear.

{{< uishot "cluster-nodes-selected" >}}

**Logs** shows the container log of a node, or its journal: one unit, or all units. **Start with** sets how many lines it shows first: 200, 1,000 or 5,000. It keeps the last 10,000. Use **Pause** to stop the stream and **Follow** to resume it.

Type in **Filter** to show only the lines with that text, or tick **Errors and warnings only**. The filter hides lines and keeps them. **Copy lines** and **Download** take the lines that show. The address keeps every choice, so a reload or a shared link shows the same log.

{{< uishot "cluster-logs" >}}

**Network and storage** lists the networks, volumes and mounts of the cluster.

{{< uishot "cluster-network" >}}

**Connect** shows the commands to open a shell on a node, to run a command, and to use `ssh`. Use **Copy** to copy one.

{{< uishot "cluster-connect" >}}

## Node

The services, events and power state of one node, and the commands for it. A worker's page also has **Drain…** and **Resume…**. **Logs** opens the log of the node: the journal of its Slurm service while it runs, otherwise the container output. Each service has a **Logs** link to its own journal.

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
