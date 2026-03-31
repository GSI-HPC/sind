---
weight: 350
title: "Diagnostics"
icon: "monitoring"
description: "Cluster health, logs, and resource inspection"
toc: true
---

## Verbose logging

By default, sind operates silently — only command output and errors are shown. The `-v` flag enables structured log output on stderr for debugging and troubleshooting.

```bash
sind -v  create cluster          # info: phase summaries
sind -vv create cluster          # debug: individual operations
sind -vvv create cluster         # trace: docker commands, probe retries
```

| Flag | Level | What's logged |
|------|-------|---------------|
| (none) | error | Errors only — commands are silent on success |
| `-v` | info | Phase transitions: "creating cluster", "nodes ready", "slurm services enabled" |
| `-vv` | debug | Individual operations: "waiting for node", "enabling slurmd", "creating network" |
| `-vvv` | trace | Docker commands, probe retry attempts with error details |

Log output goes to stderr in structured `key=value` format with colorized levels on interactive terminals, keeping stdout clean for parseable output. Colors are automatically disabled when stderr is redirected to a file or pipe.

```bash
# Capture logs while piping output
sind -v get munge-key 2>create.log | base64 -d > munge.key

# Watch creation progress
sind -vv create cluster --config cluster.yaml
```

Example output at `-vv` (colorized on interactive terminals):

```
INFO ensuring mesh infrastructure realm=sind
INFO creating cluster name=dev nodes=3
DEBU preflight check passed
INFO resolved infrastructure slurm=25.11.4
DEBU cluster resources created
DEBU waiting for node node=controller
DEBU waiting for node node=worker-0
DEBU starting readiness probes node=sind-dev-controller probes=container,systemd,sshd
DEBU all probes passed node=sind-dev-controller
INFO nodes ready count=3
DEBU enabling slurm service node=controller service=slurmctld
DEBU enabling slurm service node=worker-0 service=slurmd
INFO slurm services enabled
```

The `-v` flag belongs to the root command and must appear before the subcommand (e.g., `sind -v create cluster`).

## Cluster status

```bash
sind status [CLUSTER]
```

Displays detailed health information:

```
CLUSTER   STATUS (R/S/P/T)
dev       running (3/0/0/3)

NETWORKS
NAME             DRIVER   SUBNET           GATEWAY        STATUS
sind-mesh        bridge   172.18.0.0/16    172.18.0.1     ✓
sind-dev-net     bridge   172.19.0.0/16    172.19.0.1     ✓

MESH SERVICES
NAME   CONTAINER   STATUS
dns    sind-dns    ✓

MOUNTS
MOUNT        SOURCE               TYPE       STATUS
/etc/slurm   sind-dev-config      volume     ✓
/etc/munge   sind-dev-munge       volume     ✓
/data        /home/user/project   hostPath   ✓

NODES
NAME              ROLE        IP            CONTAINER   MUNGE  SSHD   SERVICES
controller.dev    controller  172.18.0.2    running     ✓      ✓      slurmctld ✓
worker-0.dev      worker      172.18.0.3    running     ✓      ✓      slurmd ✓
worker-1.dev      worker      172.18.0.4    running     ✓      ✓      slurmd ✗
```

The `STATUS (R/S/P/T)` column shows the cluster state followed by container counts: **R**unning, **S**topped, **P**aused, **T**otal. The cluster state is derived from the container states of all nodes:

| Status    | Meaning                                               |
|-----------|-------------------------------------------------------|
| `running` | All containers are running                            |
| `stopped` | All containers are stopped (exited, dead, or created) |
| `paused`  | All containers are paused                             |
| `mixed`   | Containers are in different states                    |
| `empty`   | No nodes exist                                        |

The cluster status reflects container health only. A running cluster can still have failing services — check the `SERVICES` column in the `NODES` table for individual service health (e.g. `slurmctld ✗`).

> **Tip:** Run `watch sind status` for a simple live dashboard that refreshes every two seconds.

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

## SSH config path

```bash
sind get ssh-config
```

Outputs the path to the realm's SSH config file. See [Node Access](../node-access/) for how to include it in `~/.ssh/config`.
