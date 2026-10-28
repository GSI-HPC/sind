---
weight: 341
title: "Pages"
description: "What each page of sind ui shows"
toc: true
---

The rail on the left lists the pages; on a phone, the bar at the bottom does, with the less used pages under **More**. The pages update by themselves. A cluster page refreshes its state while it is open, and the Overview and the lists update when Docker reports a change.

## Overview

The things that need you come first, then the realms, a card for each cluster and the recent activity. Each realm's row has **Clusters** and **Delete realm…**. An empty realm says so and offers **New cluster…**.

The head names the problems, such as `2 nodes need a look · 1 run failed`, and links to **Needs you**. A node needs a look when its container does not run, when a service on it is down, or when Slurm sends the worker no jobs, such as a drained one. The Overview, the realm table, the lists, the cluster pills and the rail count by this one rule, with the same words: the rail says `2 need a look`, and a cluster with such nodes says `running · 2 need a look`, or `stopped · 1 needs a look`. The count is red when a node does not run or a service is down, and amber when only Slurm sends a worker no jobs. When the realms differ, the label of the rail group says `counts all realms`. A node's own page says what is wrong in its pill, such as `running · slurmd down` or `running · drained`. sind ui knows that a service is down, and what Slurm says, once it has probed the cluster: every 30 seconds while any page is open, and every 10 seconds while a page of the cluster is open. A service that is down, or a worker that Slurm sends no jobs, does not count while a command works on its cluster, such as one that adds workers or reboots the node: the command may be starting it.

A cluster or node that a command creates or deletes needs no look: it is not up yet, or no longer. It shows what the command does instead. The cluster's pill says `creating · nodes 1/3`, `deleting · 8 of 11 left`, or `create queued` while the command waits for the realm lock. A node's pill says `ready`, `starting`, `queued` or `deleting`, and its tile is dashed until it is ready. Its services say `starting` or `stopping` rather than down, and its Slurm state waits until the command ends. The realm table says `creating nightly`.

{{< uishot "overview" >}}

## Clusters

Every cluster of the realm. sind keeps no list of clusters. It finds them by their Docker labels.

{{< uishot "clusters" >}}

Type in **Filter** to show only the clusters whose name, realm, state or nodes hold every word you type. **Esc** clears it. The chips above the table show the clusters that need a look, those that run, or those with monitoring, each with its count. While a command creates or deletes a cluster, a **Creating** or **Deleting** chip shows it, and **Running** leaves it out. Such a cluster has a thin progress bar under its pill and, in place of **Delete…**, **Stop…** while it is created or **Show progress** while it is deleted, and you cannot select it. Select a column head to sort by it, and again to reverse the order. Sort by state puts the clusters that need a look first. The address keeps the filter, the chip and the order, so a link or a reload shows the same list.

Tick clusters to delete several at once. **Delete 2 clusters…** in the bar (it counts them) opens one dialog that lists them, and you type their number to confirm. sind ui runs one `sind delete cluster` for each.

## Nodes

Every node of the realm. A node is named as you type it in commands, such as `worker-0.dev`.

{{< uishot "nodes" >}}

**Filter**, the chips (Needs a look, Running, Not running, and Creating or Deleting while a command does that) and the column heads work as on the Clusters page. The **Issues** column adds the reason that Slurm gives for a drained worker. Sort by state puts the nodes that are stopped, or that have a service down, first.

On a phone, each row of a list is a card with the name and the state first.

## Cluster

The state of one cluster, in six tabs.

**Summary** starts with **Ready for jobs**: it says whether the cluster can run a job now, and checks the controller, the workers, accounting and where your files are. A check that fails names the node and links to its logs. While `sind create cluster` makes the cluster, a panel at the top shows its steps and where each node stands, such as `waits for munge · 19s of 5m`, with **Show progress** and **Stop the create…**. **Add workers…** and **Delete cluster…** are off until the create ends. **Ready for jobs** says `not yet`, each check waits, and the first commands show once the create ends ok. Beside the checks are the first two commands of a job, `sind enter` and `sbatch`, with **Copy**. A check of the workers offers a fix for each broken worker, such as **Reboot worker-4…** or **Resume worker-5…**. Below are the nodes as tiles, with a legend of what each tile means (a ring for a service down, an amber ring for a worker that takes no jobs), a matrix of nodes and services, such as munge, sshd and slurmctld, with a **Slurm** column for the state of each worker, and the network checks. Each tile and each cell of the matrix has its state in words for screen readers and as a tooltip, such as `worker-4.dev, running, slurmd down`. The **Nodes** tab keeps the number of nodes and adds how many need a look, such as `2 need a look`.

{{< uishot "cluster" >}}

**Nodes** lets you select nodes. The selection appears as a node set, such as `worker-[2-4]`. Power and delete act on the selection, and the bar offers only the actions that fit it: **Start…** for nodes that do not run, **Shut down…** for nodes that run, **Drain…**, **Resume…** and delete for workers. When an action fits only some of the nodes, its label counts them, such as **Start 1 node…**, and it acts on those. The delete button names the workers, such as **Delete worker-5…** or **Delete 3 workers…**. Each row has **Start…** (or **Unfreeze…** for a frozen node), except while a command creates or deletes the node, **Logs**, which opens the Logs tab on the node, and **Shell**, which copies the command that opens a shell on it. The **Slurm** column shows the state that `sinfo` reports for each worker, such as `idle`, `allocated` or `drained`, and the reason.

{{< uishot "cluster-nodes-selected" >}}

**Slurm** shows what Slurm says: the partitions, the state, reason, CPUs and memory of each worker, and the `slurm.conf` and `sind-nodes.conf` that sind wrote, each with **Copy**. **View slurm.conf** on the Summary leads here. sind ui runs `sinfo` on the controller each time it reads the status of the cluster, and gives it 10 seconds. When slurmctld does not answer, the tab says why and still shows the files, which the controller reads without slurmctld. A worker that a command creates or deletes shows the command's state in place of its Slurm state and reason, such as `starting` and `waits for munge · 19s of 5m`.

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

The mesh network, the DNS server and its records, and the containers that are connected to the realm. Type into the filter above the records to find a name or an address. **Copy** on a row copies its name, and **Copy list** copies the records that show, as lines of `/etc/hosts`; when the browser copies nothing, the lines show in a box to copy with Ctrl+C. A name of a node opens the node. Below the records is a `dig` command that asks the DNS server from the host.

{{< uishot "mesh" >}}

## Monitoring

The key numbers of each monitored cluster, and the state and addresses of Grafana, Prometheus, Loki and Alloy, if the realm runs them. For each cluster, the page shows its node containers that run, and from Prometheus the Slurm nodes that take jobs (**Take jobs**: neither down nor in drain, as **Ready for jobs** counts them), with how many are down and how many are in drain (drained or draining: Slurm sends them no new jobs), and the running and pending jobs. Prometheus has these numbers once it has read slurmctld, a short time after the create. Grafana has the rest.

**Open** uses the names under `.sind`, which resolve only on the Docker host, with [host DNS]({{< relref "/architecture/networking#host-dns-resolution" >}}). Each service also shows its address with the port, with **Copy**. When Doctor says that the host does not resolve the names, the page says that **Open** fails and to use the address. From another machine, forward the address with `ssh -L`, as the page shows for Grafana.

The buttons **Read every** 10s, 30s, 1m and 5m set how often sind ui reads the stacks and their numbers while a page is open. A press saves the interval, and the page says **Saved**. The change lasts until sind ui stops. It does not change how often Prometheus scrapes.

{{< uishot "monitoring" >}}

## Topology

A diagram from the host, through the mesh services, to the cluster networks and the nodes. Each node shows its address, its role and its state, and each box opens its page. **Show as Table** lists the same things with the same links, as rows: the host and the containers on the mesh, then each cluster network with its nodes. On a narrow screen the diagram scrolls sideways, and a line above it says so; the Tab key then stops on it, and the arrow keys scroll it. On a phone the table shows each row as a card.

{{< uishot "topology" >}}

## Doctor

The host checks of `sind doctor`, each as ok, warning or failed, with the fix for each. The line at the top says whether sind can create clusters on this host: a failed check stops create and power on, a warning does not. A fix shows each command in its own block, after the words that say what it does, and **Copy** copies only the command. The fix for inotify has a second command that keeps the limit across reboots.

{{< uishot "doctor" >}}

## Activity

The runs of sind commands that change something, from this UI or from a terminal. A run opens its run page (see [Runs and Progress]({{< relref "/web-ui/runs" >}})).

{{< uishot "activity" >}}

## Help

Every how-to of the help pane, with links to all words and to the keys. The rail has no entry for it: open it from **All how-tos** in the help pane, or at the address `/help`. An entry of the help pane has an address of its own, such as `/help?topic=word/realm`, and the search of the page, such as `/help?q=sbatch`, works without script (see [Find things and get help]({{< relref "/web-ui#find-things-and-get-help" >}})).
