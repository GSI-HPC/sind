---
weight: 341
title: "Pages"
description: "What each page of sind ui shows"
toc: true
---

The rail on the left lists the pages. The pages update by themselves. A cluster page refreshes its state while it is open, and the Overview and the lists update when Docker reports a change.

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

**Nodes** lets you select nodes. The selection appears as a node set, such as `worker-[2-4]`. Power and delete act on the selection.

{{< uishot "cluster-nodes-selected" >}}

**Logs** shows the container log of a node, or the journal of one of its services. It starts with the last 200 lines and keeps the last 2,000. Use **Pause** to stop the stream and **Follow** to resume it.

{{< uishot "cluster-logs" >}}

**Network and storage** lists the networks, volumes and mounts of the cluster.

{{< uishot "cluster-network" >}}

**Connect** shows the commands to open a shell on a node, to run a command, and to use `ssh`. Use **Copy** to copy one.

{{< uishot "cluster-connect" >}}

## Node

The services, events and power state of one node, and the commands for it.

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
