---
weight: 73
title: "03 · Inside a sind cluster"
description: "Roles, networks, DNS and the SSH relay, drawn and then explored with get and ssh"
---

{{< video "course-03-inside-a-cluster" >}}

Before you trust a tool with your Docker host, and long before you debug a cluster that misbehaves, you want to know what it created. This episode draws a sind cluster's roles, networks, DNS and SSH relay, then finds each of them with `sind get` and `sind ssh`.

## In this episode

- The five node roles, each one container with systemd inside: [Node Definitions]({{< relref "/configuration/node-definitions#node-roles" >}})
- The cluster network and the realm's mesh: [Networking]({{< relref "/architecture/networking#cluster-network" >}})
- Node names from `sind-dns`: [DNS]({{< relref "/architecture/networking#dns" >}})
- Inspecting nodes, networks and DNS records: [Diagnostics]({{< relref "/usage/diagnostics" >}})
- `sind ssh` through the `sind-ssh` relay: [Node Access]({{< relref "/usage/node-access#ssh" >}})

## Commands

Every command and output in the video, in order, as on the reference pages.

Create the default cluster, one controller and one worker ([Quickstart]({{< relref "/getting-started/quickstart" >}})):

```bash
sind create cluster
```

List the node containers ([Diagnostics]({{< relref "/usage/diagnostics#list-nodes" >}})):

```bash
sind get nodes
```

```text
CONTAINER                CLUSTER   ROLE         FQDN                            IP           STATUS
sind-default-controller  default   controller   controller.default.sind.sind    172.19.0.2   running
sind-default-worker-0    default   worker       worker-0.default.sind.sind      172.19.0.3   running
```

List the networks and the DNS records ([Diagnostics]({{< relref "/usage/diagnostics#list-networks" >}})):

```bash
sind get networks
```

```text
NAME              DRIVER   SUBNET           GATEWAY
sind-default-net  bridge   172.19.0.0/16    172.19.0.1
sind-mesh         bridge   172.18.0.0/16    172.18.0.1
```

```bash
sind get dns
```

```text
HOSTNAME                              IP
controller.default.sind.sind         172.19.0.2
worker-0.default.sind.sind           172.19.0.3
```

Open a shell on the controller through the SSH relay ([Node Access]({{< relref "/usage/node-access#ssh" >}})):

```bash
sind ssh controller
```

## Refresher: Docker networks and DNS

Containers on one network reach each other by name, separate bridges are isolated, and a container can join several networks: [the full refresher]({{< relref "refreshers#docker-networks-and-dns" >}}).

## Go deeper

- [Networking]({{< relref "/architecture/networking" >}}): gateway priority, the pinned DNS address, host DNS and the SSH key lifecycle
- [Docker Resources]({{< relref "/architecture/docker-resources" >}}): every container, network, volume and label sind creates
- [Design Overview]({{< relref "/architecture/overview" >}}): systemd in each node and the creation flow
- [Diagnostics]({{< relref "/usage/diagnostics" >}}): when something breaks, `sind get cluster` for the cluster's health and `sind logs` for a service's journal, with a short clip
- [Realms]({{< relref "/configuration/realms" >}}): several sind setups side by side, with a short clip
- [Node Access]({{< relref "/usage/node-access" >}}): `sind enter`, `sind exec` and your own `ssh` client with the exported SSH config
