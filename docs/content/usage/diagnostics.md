---
weight: 350
title: "Diagnostics"
icon: "monitoring"
description: "Cluster health, logs, and resource inspection"
toc: true
---

## Cluster status

```bash
sind status [CLUSTER]
```

Displays detailed health information:

```
Cluster: dev
Status:  running

NODES
NAME              ROLE        IP            CONTAINER   MUNGE  SSHD   SERVICES
controller.dev    controller  172.18.0.2    running     ✓      ✓      slurmctld ✓
worker-0.dev      worker      172.18.0.3    running     ✓      ✓      slurmd ✓
worker-1.dev      worker      172.18.0.4    running     ✓      ✓      slurmd ✗

NETWORK
Mesh:     sind-mesh ✓  172.18.0.0/16  gw 172.18.0.1
DNS:      sind-dns ✓
Cluster:  sind-dev-net ✓  172.19.0.0/16  gw 172.19.0.1

VOLUMES
sind-dev-config ✓
sind-dev-munge ✓
sind-dev-data ✓
```

## Logs

```bash
sind logs NODE [SERVICE] [--follow|-f]
```

Without a `SERVICE` argument, shows container logs (stdout/stderr). With a service name, shows journalctl output for that systemd unit.

```bash
# Container logs
sind logs controller
sind logs controller --follow

# Service logs
sind logs controller slurmctld
sind logs worker-0 slurmd --follow
```

## List nodes

```bash
sind get nodes [CLUSTER]
```

```
NAME                 ROLE         STATUS
controller.default   controller   running
worker-0.default     worker       running
worker-1.default     worker       running
```

## List networks

```bash
sind get networks
```

```
NAME              DRIVER   SUBNET           GATEWAY
sind-mesh         bridge   172.18.0.0/16    172.18.0.1
sind-default-net  bridge   172.19.0.0/16    172.19.0.1
```

## List volumes

```bash
sind get volumes
```

```
NAME                  DRIVER
sind-default-config   local
sind-default-munge    local
sind-default-data     local
sind-ssh-config       local
```

## DNS records

```bash
sind get dns
```

```
HOSTNAME                              IP
controller.default.sind.local         172.19.0.2
worker-0.default.sind.local           172.19.0.3
```

## Munge key

```bash
sind get munge-key [CLUSTER]
```

Outputs the cluster's munge key encoded as base64, suitable for injection into external tooling.
