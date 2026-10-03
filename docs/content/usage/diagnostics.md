---
weight: 350
title: "Diagnostics"
icon: "monitoring"
description: "Cluster health, logs, and resource inspection"
toc: true
---

## Doctor

```bash
sind doctor
```

Checks system prerequisites and reports pass/fail for each:

```
✓ Docker Engine: 28.1.1 (>= 28.0)
✓ cgroupv2: nsdelegate enabled (/sys/fs/cgroup)
✓ DNS policy: host resolution available
```

| Check | Required | Description |
|-------|----------|-------------|
| Docker Engine | yes | Docker >= 28.0 reachable (`docker info`) |
| cgroupv2 | yes | cgroup2 mounted with `nsdelegate` option |
| DNS policy | no | polkit authorization for host DNS resolution via systemd-resolved |

The results go to stdout, so they can be piped or filtered; only the error line naming the failed checks goes to stderr. When a required check fails, `sind doctor` exits with a non-zero status; for a missing `nsdelegate` it also prints the commands that enable it. When Docker is not reachable, the check quotes the first line of docker's error and, for the common causes, says how to fix them:

```
✗ Docker Engine: not reachable: permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock: ...

Add your user to the docker group, then log in again (or run newgrp docker):

sudo usermod -aG docker $USER
```

A missing `docker` CLI and a daemon that is not running get their own hint. The DNS policy check is advisory — it only appears when systemd-resolved is running, and failure does not affect the exit status. When the DNS check fails, `sind doctor` prints two polkit rule profiles (desktop and server) with copyable install commands — see [Polkit policy](../../architecture/networking/#polkit-policy) for details.

Example output when `nsdelegate` is missing:

```
✗ cgroupv2: nsdelegate not found

Enable nsdelegate temporarily:

sudo mount -o remount,nsdelegate /sys/fs/cgroup

Enable nsdelegate on boot (systemd):

sudo mkdir -p /etc/systemd/system/sys-fs-cgroup.mount.d
echo -e '[Mount]\nOptions=nsdelegate' \
  | sudo tee /etc/systemd/system/sys-fs-cgroup.mount.d/nsdelegate.conf
sudo systemctl daemon-reload
```

### Machine-readable output

`sind doctor -o json` prints the checks as a JSON array for scripts. Each check has a `name`, a `status` (`ok`, `failed`, or `warning` when the advisory DNS policy check does not pass) and a `detail`; a check that did not pass and has a fix adds the commands as `remediation`. The exit status is the same as with the default output.

```bash
sind doctor -o json
```

```json
[
  {
    "name": "Docker Engine",
    "status": "ok",
    "detail": "28.1.1 (>= 28.0)"
  },
  {
    "name": "cgroupv2",
    "status": "failed",
    "detail": "nsdelegate not found",
    "remediation": "Enable nsdelegate temporarily:\n\nsudo mount -o remount,nsdelegate /sys/fs/cgroup\n..."
  }
]
```

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
| `-vv` | debug | Individual operations: "waiting for node", "starting readiness probes", "enabling slurm service" |
| `-vvv` | trace | Docker commands, probe retry attempts with error details |

Log output goes to stderr in structured `key=value` format with timestamps and colorized levels on interactive terminals, keeping stdout clean for parseable output. Colors are automatically disabled when stderr is redirected to a file or pipe.

```bash
# Capture logs while piping output
sind -v get auth-key 2>create.log | base64 -d > munge.key

# Watch creation progress
sind -vv create cluster --config cluster.yaml
```

Example output at `-vv` (colorized on interactive terminals):

```
00:13:28.438 INFO ensuring mesh infrastructure realm=sind
00:13:28.826 INFO creating cluster name=dev nodes=3
00:13:28.921 DEBU preflight check passed
00:13:29.382 INFO resolved infrastructure slurm=25.11.8
00:13:30.149 DEBU cluster resources created
00:13:30.621 DEBU waiting for node node=controller
00:13:30.622 DEBU waiting for node node=worker-0
00:13:30.623 DEBU starting readiness probes node=sind-dev-controller probes=container,systemd,sshd,munge
00:13:31.252 DEBU all probes passed node=sind-dev-controller
00:13:31.474 INFO nodes ready count=3
00:13:32.362 DEBU enabling slurm service node=controller service=slurmctld
00:13:32.363 DEBU enabling slurm service node=worker-0 service=slurmd
00:13:32.608 INFO slurm services enabled
```

`-v` is a global flag and may come before or after the subcommand (`sind -v create cluster`, `sind create cluster -v`). `sind ssh` passes its arguments through to SSH, so for it `-v` must come first: `sind -v ssh worker-0` logs sind's steps, while `sind ssh -v worker-0` makes SSH verbose. The same holds for `--realm`. `sind exec` takes both anywhere before its `--`.

## JSON output

Every `sind get` subcommand accepts a persistent `--output|-o {human,json}` flag. The default is `human` (tabular text); `json` emits a machine-readable document for scripting and automation. `sind doctor` takes the same flag (see [Machine-readable output](#machine-readable-output)).

```bash
sind get clusters -o json
sind get nodes -o json
sind get cluster dev --output json
```

## Cluster status

```bash
sind get cluster [NAME]
```

Displays detailed health information:

```
CLUSTER   SLURM     STATUS (R/S/P/T)
dev       25.11.8   running (4/0/0/4)

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
NAME              ROLE        IP            STATUS    SERVICES
controller.dev    controller  172.19.0.2    running   munge ✓ slurmctld ✓ sshd ✓
db.dev            db          172.19.0.5    running   mariadb ✓ munge ✓ slurmdbd ✓ sshd ✓
worker-0.dev      worker      172.19.0.3    running   munge ✓ slurmd ✓ sshd ✓
worker-1.dev      worker      172.19.0.4    running   munge ✓ slurmd ✗ sshd ✓
```

`SLURM` shows `-` when sind does not know the version, as for unmanaged clusters. The `STATUS (R/S/P/T)` column shows the cluster state followed by container counts: **R**unning, **S**topped, **P**aused, **T**otal. The cluster state is derived from the container states of all nodes:

| Status    | Meaning                                               |
|-----------|-------------------------------------------------------|
| `running` | All containers are running                            |
| `stopped` | All containers are stopped (exited, dead, or created) |
| `paused`  | All containers are paused                             |
| `mixed`   | Containers are in different states                    |
| `empty`   | No nodes exist                                        |
| `unknown` | All containers are in a state sind does not map, e.g. `restarting` |

The cluster status reflects container health only. A running cluster can still have failing services — check the `SERVICES` column in the `NODES` table for individual service health (e.g. `slurmctld ✗`).

Nodes where sind does not manage Slurm (unmanaged workers and db nodes, and every node of an [unmanaged cluster]({{< relref "/guides/unmanaged-cluster" >}})) list only `munge` and `sshd`. With [identity `clientIds`]({{< relref "/guides/users#clientids" >}}), no node lists `munge`, as auth/slurm replaces it, and the submitter lists `sackd`; `MOUNTS` has no `/etc/munge`. The JSON output marks each node with `"managed": true|false`, as does `sind get node -o json`.

The data row shows what Docker mounts at the nodes' data mount point: the bind-mounted host directory (type `hostPath`) or the volume. With [`users`]({{< relref "/configuration/cluster-config#users-section" >}}), `MOUNTS` lists the home volume at `/home`. With [`storage.cvmfs`]({{< relref "/guides/cvmfs" >}}), `MOUNTS` lists `/cvmfs` too: source `cvmfs` of type `volume` from the volume plugin, or source `/cvmfs` of type `hostPath` from the Docker host.

Clusters with a [backup controller]({{< relref "/guides/controller-failover" >}}) get an `HA` column in the `NODES` table: `primary` or `backup` for each controller, with `*` on the one in control. The shared state volume appears under `MOUNTS` as `/var/spool/slurmctld`. Unmanaged clusters show no `HA` column: sind cannot tell which of their controllers is in control.

> **Tip:** Run `watch sind get cluster` for a simple live dashboard that refreshes every two seconds.

## Node status

```bash
sind get node NODE[.CLUSTER]
```

Shows the health of a single node. `NODE` is a short name, optionally followed by its cluster (`worker-0.dev`); the cluster defaults to `default`. A full DNS name ending in `.sind` is rejected.

```
CONTAINER             ROLE         FQDN                       IP           STATUS
sind-dev-controller   controller   controller.dev.sind.sind   172.19.0.2   running

SERVICES
NAME        STATUS
munge       ✓
slurmctld   ✓
sshd        ✓
```

Controllers of a backup pair get the same `HA` column as in `sind get cluster`.

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
sind logs submitter sackd             # identity clientIds
sind logs db slurmdbd
```

## List nodes

```bash
sind get nodes [CLUSTER]
```

Without a `CLUSTER` argument, lists every node in the realm with an extra `CLUSTER` column; with a cluster name, scopes to that cluster and drops the column. Rows are sorted by `(cluster, role, natural-name)` so `worker-2` precedes `worker-10`.

```
CONTAINER                CLUSTER   ROLE         FQDN                            IP           STATUS
sind-default-controller  default   controller   controller.default.sind.sind    172.19.0.2   running
sind-default-worker-0    default   worker       worker-0.default.sind.sind      172.19.0.3   running
sind-default-worker-1    default   worker       worker-1.default.sind.sind      172.19.0.4   running
```

## List networks

```bash
sind get networks
```

```
NAME              DRIVER   SUBNET           GATEWAY
sind-default-net  bridge   172.19.0.0/16    172.19.0.1
sind-mesh         bridge   172.18.0.0/16    172.18.0.1
```

## List realms

```bash
sind get realms
```

```
NAME   CLUSTERS
ci     1
sind   2
```

Lists all active realms discovered from Docker container labels. Useful when working with multiple realms to see what's running.

## List volumes

```bash
sind get volumes
```

```
NAME                  DRIVER
sind-default-config   local
sind-default-data     local
sind-default-munge    local
sind-ssh-config       local
```

## DNS records

```bash
sind get dns
```

```
HOSTNAME                              IP
controller.default.sind.sind         172.19.0.2
worker-0.default.sind.sind           172.19.0.3
```

## Authentication key

```bash
sind get auth-key [CLUSTER]
```

Outputs the key that authenticates the cluster's Slurm traffic, encoded as base64, suitable for injection into external tooling: the munge key, or `slurm.key` with [identity `clientIds`]({{< relref "/guides/users#clientids" >}}). `-o json` returns it with its type: `{"type": "munge", "key": "..."}` or `{"type": "slurm", "key": "..."}`. `sind get auth-key` replaces `sind get munge-key`.

## Mesh infrastructure

```bash
sind get mesh
```

Shows the realm's mesh infrastructure: network name, DNS container/IP/zone/image, and the SSH container/volume/image. Useful for external consumers that need to connect to sind networks without reimplementing the naming conventions.

If no mesh has been created yet, the command returns a friendly `no mesh found for realm "<name>"` error.

## SSH credentials

```bash
sind get ssh-private-key
sind get ssh-public-key
sind get ssh-known-hosts
```

Dump the realm's SSH credentials to stdout. With `-o json`, each command emits a distinct top-level field (`private_key`, `public_key`, `known_hosts`) so consumers don't need to special-case the same JSON key for three payload types. Together with `sind get ssh-config`, this replaces the need to extract files from Docker volumes.

## SSH config path

```bash
sind get ssh-config
```

Outputs the path to the realm's SSH config file. See [Node Access](../node-access/) for how to include it in `~/.ssh/config`.
