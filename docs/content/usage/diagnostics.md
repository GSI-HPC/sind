---
weight: 350
title: "Diagnostics"
description: "Cluster health, logs, and resource inspection"
toc: true
---

## Doctor

{{< ways >}}
{{< cli >}}

```bash
sind doctor
```

{{< /cli >}}
{{< ui >}}

Open **Doctor** in the rail. The line at the top says whether sind can create clusters on this host. Each check shows as ok, warning or failed, with the fix. **Copy** beside a command copies only the command.

{{< uishot "doctor" >}}

{{< /ui >}}
{{< /ways >}}

Checks system prerequisites and reports pass/fail for each:

```
✓ Docker Engine: 28.1.1 (>= 28.0)
✓ Docker daemon: rootful, no userns-remap
✓ Docker host: this machine (unix:///var/run/docker.sock)
✓ cgroupv2: nsdelegate enabled (/sys/fs/cgroup)
✓ inotify: max_user_instances 8192 (>= 1024)
✓ DNS policy: host resolution available
```

| Check | Required | Description |
|-------|----------|-------------|
| Docker Engine | yes | Docker >= 28.0 reachable (`docker info`) |
| Docker daemon | yes | rootful, without `userns-remap`: Docker refuses the writable cgroups of sind's nodes in rootless mode and with `userns-remap` |
| Docker host | no | the daemon runs on this machine, not in Docker Desktop's VM or on another host |
| cgroupv2 | yes | Docker runs containers on cgroup v2 (`docker info`), and the Docker host mounts the unified cgroup2 hierarchy at `/sys/fs/cgroup` with the `nsdelegate` option |
| inotify | no | `fs.inotify.max_user_instances` of at least 1024, which clusters of 10 or more nodes need |
| DNS policy | no | polkit authorization for host DNS resolution via systemd-resolved |

The results go to stdout, so they can be piped or filtered, with `✓` for a passed check, `✗` for a failed one and `!` for a warning; only the error line naming the failed checks, as the output prints them, goes to stderr. When a required check fails, `sind doctor` exits with a non-zero status; for a missing `nsdelegate` it also prints the commands that enable it. The fixes follow the list of checks, each one once, in the order of the checks that need it. When Docker is not reachable, the check quotes the first line of docker's error and, for the common causes, says how to fix them:

```
✗ Docker Engine: not reachable: permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock: ...
✓ cgroupv2: nsdelegate enabled (/sys/fs/cgroup)

Add your user to the docker group, then log in again (or run newgrp docker):

sudo usermod -aG docker $USER
```

A missing `docker` CLI and a daemon that is not running get their own hint. Every other command fails on such a host with one line that points here:

```console
$ sind get cluster
12:00:00.000 ERRO Docker is not reachable: Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?; run `sind doctor` for the fix
```

A host in systemd's hybrid mode, which mounts cgroup2 only at `/sys/fs/cgroup/unified` and runs containers on cgroup v1, fails the cgroupv2 check with the kernel option that switches to the unified hierarchy. A daemon in rootless mode or with `userns-remap` fails the Docker daemon check, with the way back to a rootful daemon; `sind create cluster` refuses such a daemon too, before it pulls an image. The Docker host, inotify and DNS policy checks are advisory: a warning does not affect the exit status. The inotify check prints the `sysctl` commands that raise the limit; it is left out when the limit cannot be read or the daemon does not run on this machine. The DNS policy check only appears when systemd-resolved is running; when the daemon does not run on this machine it reports host resolution as not available, since the mesh bridge that the host resolver would use is on the daemon's host. When the DNS check fails, `sind doctor` prints two polkit rule profiles (desktop and server) with copyable install commands — see [Polkit policy](../../architecture/networking/#polkit-policy) for details.

Example output when `nsdelegate` is missing:

```
✗ cgroupv2: nsdelegate not found

Enable nsdelegate on the Docker host temporarily:

sudo mount -o remount,nsdelegate /sys/fs/cgroup

Enable nsdelegate on boot (systemd):

sudo mkdir -p /etc/systemd/system/sys-fs-cgroup.mount.d
echo -e '[Mount]\nOptions=nsdelegate' \
  | sudo tee /etc/systemd/system/sys-fs-cgroup.mount.d/nsdelegate.conf
sudo systemctl daemon-reload
```

`sind create cluster` and `sind create worker` check `nsdelegate` too, before they create a node container, and fail with the same commands. They read it in a throwaway container of the node image, which shows the option of the kernel the Docker daemon runs on, wherever that is.

### Docker host

sind runs on the Docker host itself. Docker Desktop, whose daemon runs in a VM, and a daemon on another host, reached through `DOCKER_HOST`, `DOCKER_CONTEXT` or `docker context use`, are not supported: `--data` paths name directories on the daemon's host, host DNS cannot reach the mesh bridge there, and the inotify check would read this machine's limit. The Docker host check tells where the daemon runs, from the endpoint the docker CLI uses and from `docker info`; a daemon that runs another kernel than this machine counts as elsewhere too. A CI job in a container that uses the host's Docker socket shares the host's kernel and counts as local.

For a daemon elsewhere, the check is a warning that says why, with the way back to a local daemon:

```
! Docker host: not this machine: docker context "build" connects to ssh://ci@build-host, not a unix socket (unsupported)

sind expects to run on the Docker host: --data paths, host DNS for the
*.sind names and the inotify check refer to this machine, the nodes to the
daemon's. Run sind on the Docker host, or point the docker CLI at a daemon
on this machine:

unset DOCKER_HOST DOCKER_CONTEXT
docker context use default
```

It does not fail `sind doctor`: the detection can be wrong both ways, as a socket can be forwarded from another host and a TCP endpoint can be this machine's own daemon, and the required checks still hold, as they ask the daemon. The cgroupv2 check then reads `nsdelegate` in a throwaway container of the default node image, if the daemon has that image; `sind doctor` pulls none, and without it the check is a warning, `nsdelegate not checked`, with the `docker pull` that lets it check.

### Machine-readable output

`sind doctor -o json` prints the checks as a JSON array for scripts. Each check has a `name` (`Docker Engine`, `Docker daemon`, `Docker host`, `cgroupv2`, `inotify`, `DNS policy`), a `status` (`ok`, `failed`, or `warning` when an advisory check does not pass or the cgroupv2 check could not read `nsdelegate`) and a `detail`; a check that did not pass and has a fix adds the commands as `remediation`. The exit status is the same as with the default output.

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
    "name": "Docker daemon",
    "status": "ok",
    "detail": "rootful, no userns-remap"
  },
  {
    "name": "Docker host",
    "status": "ok",
    "detail": "this machine (unix:///var/run/docker.sock)"
  },
  {
    "name": "cgroupv2",
    "status": "failed",
    "detail": "nsdelegate not found",
    "remediation": "Enable nsdelegate on the Docker host temporarily:\n\nsudo mount -o remount,nsdelegate /sys/fs/cgroup\n..."
  },
  {
    "name": "inotify",
    "status": "ok",
    "detail": "max_user_instances 8192 (>= 1024)"
  }
]
```

## Version

```bash
sind version
```

Prints the version and, for a release build, the commit it was built from:

```
sind 0.12.0 (0a57d68)
```

`sind --version` prints the same line.

`--json` prints all fields as one JSON object, for scripts such as [sind-action](https://github.com/GSI-HPC/sind-action), which reads `version`:

```bash
sind version --json
```

```json
{"version":"0.12.0","commit":"0a57d68","goVersion":"go1.26.8","platform":"linux/amd64"}
```

| Field | Description |
|-------|-------------|
| `version` | sind's version, without the `v` of the release tag |
| `commit` | the commit the binary was built from; left out when unknown |
| `goVersion` | the Go toolchain that built the binary |
| `platform` | operating system and architecture, `linux/amd64` or `linux/arm64` |

A development build reports the output of `git describe`, such as `0.12.0-3-gabc1234-dirty`; see [Make targets]({{< relref "/contributing/development-setup#make-targets" >}}) for how `make build`, `go install` and `go build` set the version.

## Verbose logging

By default, sind logs only errors. Besides them it shows only command output and, on a terminal, the [progress]({{< relref "/usage/progress" >}}) of `create`, `delete` and `power`. The `-v` flag enables structured log output on stderr for debugging and troubleshooting.

```bash
sind -v  create cluster          # info: phase summaries
sind -vv create cluster          # debug: individual operations
sind -vvv create cluster         # trace: docker commands, probe retries
```

| Flag | Level | What's logged |
|------|-------|---------------|
| (none) | error | Errors only |
| `-v` | info | Phase transitions: "creating cluster", "nodes ready", "slurm services enabled" |
| `-vv` | debug | Individual operations: "waiting for node", "starting readiness probes", "enabling slurm service" |
| `-vvv` | trace | Docker commands, probe retry attempts with error details |

Log output goes to stderr in structured `key=value` format with timestamps and colorized levels on interactive terminals, keeping stdout clean for parseable output. Colors are automatically disabled when stderr is redirected to a file or pipe. On a terminal, the log lines of `create`, `delete` and `power` go above their progress display, never into it.

```bash
# Capture logs while piping output
sind -v get auth-key 2>create.log | base64 -d > munge.key

# Log each operation of a creation
sind -vv create cluster --config cluster.yaml

# Follow a creation as plain lines, without a terminal
sind create cluster --config cluster.yaml --progress plain 2>&1 | tee create.log
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

`-v` is a global flag and may come before or after the subcommand (`sind -v create cluster`, `sind create cluster -v`). `sind ssh` passes its arguments through to SSH, so for it `-v` must come first: `sind -v ssh worker-0` logs sind's steps, while `sind ssh -v worker-0` makes SSH verbose. The same holds for `--realm`, `--progress` and `--progress-log`. `sind exec` takes them anywhere before its `--`.

## JSON output

Every `sind get` subcommand accepts a persistent `--output|-o {human,json}` flag. The default is [`SIND_OUTPUT`]({{< relref "/usage/environment#output" >}}), else `human` (tabular text); `json` emits a machine-readable document for scripting and automation. `sind doctor` takes the same flag (see [Machine-readable output](#machine-readable-output)).

```bash
sind get clusters -o json
sind get nodes -o json
sind get cluster dev --output json
```

Listings are sorted by name in natural order, with numbers by their value, so the cluster `dev2` comes before `dev10`; `sind get dns` sorts by host name. A listing that finds nothing prints its header alone on stdout and says so on stderr, with where sind looked, such as `No clusters found in realm "sind".`; with `-o json` it prints an empty list and nothing on stderr. `sind get ssh-config` notes on stderr, with `Note:`, that the file it prints the path of does not exist yet.

## Cluster status

{{< ways >}}
{{< cli >}}

```bash
sind get cluster [NAME]
```

{{< /cli >}}
{{< ui >}}

Open the cluster. The **Summary** tab shows each node and its services, and the network checks.

{{< uishot "cluster" >}}

{{< /ui >}}
{{< /ways >}}

Displays detailed health information:

```
CLUSTER  SLURM    STATUS (R/S/P/T)
dev      25.11.8  running (4/0/0/4)

NETWORKS
NAME          DRIVER  SUBNET         GATEWAY     STATUS
sind-mesh     bridge  172.18.0.0/16  172.18.0.1  ✓
sind-dev-net  bridge  172.19.0.0/16  172.19.0.1  ✓

MESH SERVICES
NAME  CONTAINER  STATUS
dns   sind-dns   ✓
ssh   sind-ssh   ✓

MOUNTS
MOUNT       SOURCE              TYPE      STATUS
/etc/slurm  sind-dev-config     volume    ✓
/etc/munge  sind-dev-munge      volume    ✓
/data       /home/user/project  hostPath  ✓

NODES
NAME            ROLE        IP          STATUS   SERVICES
controller.dev  controller  172.19.0.2  running  munge ✓ slurmctld ✓ sshd ✓
db.dev          db          172.19.0.5  running  mariadb ✓ munge ✓ slurmdbd ✓ sshd ✓
worker-0.dev    worker      172.19.0.3  running  munge ✓ slurmd ✓ sshd ✓
worker-1.dev    worker      172.19.0.4  running  munge ✓ slurmd ✗ sshd ✓
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

A node's `STATUS`, in `sind get cluster`, `sind get node` and `sind get nodes`, and its `status` in their JSON output, uses the same words for its container:

| Status    | Container state                     |
|-----------|-------------------------------------|
| `running` | running                             |
| `stopped` | exited, dead, or created            |
| `paused`  | paused                              |
| `unknown` | another state, e.g. `restarting`    |

The `S` count of `STATUS (R/S/P/T)` includes the `unknown` nodes.

`MESH SERVICES` shows ✓ for the realm's DNS container and SSH relay while they run (`dns_ok` and `ssh_ok` in the JSON output). After a host reboot or a Docker daemon restart they exist but are stopped; `sind power on` starts them again (see [Power Control]({{< relref "/usage/power-control#power-on" >}})).

When the realm has a [monitoring stack]({{< relref "/guides/monitoring" >}}), `MESH SERVICES` adds its four containers, each with ✓ while the service answers its readiness check, or while its container runs when Grafana, which runs the checks, does not (`grafana_ok`, `prometheus_ok`, `loki_ok` and `alloy_ok` in the JSON output, which leaves them out without a stack). A monitored cluster lists its journal mount under `MOUNTS`: the realm's journal volume and the cluster's directory in it, in which each node mounts its own (`"subpath": "dev"` in the JSON output). The JSON output has `"monitoring": true` for a monitored cluster.

```
MESH SERVICES
NAME        CONTAINER        STATUS
dns         sind-dns         ✓
ssh         sind-ssh         ✓
grafana     sind-grafana     ✓
prometheus  sind-prometheus  ✓
loki        sind-loki        ✓
alloy       sind-alloy       ✓

MOUNTS
MOUNT             SOURCE                   TYPE      STATUS
/etc/slurm        sind-dev-config          volume    ✓
/etc/munge        sind-dev-munge           volume    ✓
/data             /home/user/project       hostPath  ✓
/var/log/journal  sind-journal:dev/<node>  volume    ✓
```

The cluster status reflects container health only. A running cluster can still have failing services — check the `SERVICES` column in the `NODES` table for individual service health (e.g. `slurmctld ✗`).

A managed [api node]({{< relref "/configuration/node-definitions#api-node" >}}) lists `slurmrestd`. Nodes where sind does not manage Slurm (unmanaged workers, db and api nodes, and every node of an [unmanaged cluster]({{< relref "/guides/unmanaged-cluster" >}})) list only `munge` and `sshd`. With [identity `clientIds`]({{< relref "/guides/users#clientids" >}}), no node lists `munge`, as auth/slurm replaces it, and the submitter lists `sackd`; `MOUNTS` has no `/etc/munge`. The JSON output marks each node with `"managed": true|false`, as does `sind get node -o json`.

The data row shows what Docker mounts at the nodes' data mount point: the bind-mounted host directory (type `hostPath`) or the volume. With [`users`]({{< relref "/configuration/cluster-config#users-section" >}}), `MOUNTS` lists the home volume at `/home`. With [`storage.cvmfs`]({{< relref "/guides/cvmfs" >}}), `MOUNTS` lists `/cvmfs` too: source `cvmfs` of type `volume` from the volume plugin, or source `/cvmfs` of type `hostPath` from the Docker host.

Clusters with a [backup controller]({{< relref "/guides/controller-failover" >}}) get an `HA` column in the `NODES` table: `primary` or `backup` for each controller, with `*` on the one in control, and `-` for the other nodes. The shared state volume appears under `MOUNTS` as `/var/spool/slurmctld`. Unmanaged clusters show no `HA` column: sind cannot tell which of their controllers is in control.

> **Tip:** Run `watch sind get cluster` for a simple live dashboard that refreshes every two seconds.

## Node status

{{< ways >}}
{{< cli >}}

```bash
sind get node NODE[.CLUSTER]
```

{{< /cli >}}
{{< ui >}}

Open the node from the **Nodes** page, or from the **Nodes** tab of its cluster. Its page shows its services, what Slurm says about it and what changed on it. When it needs a look, a card at the top says what failed and when, shows the last lines of the journal of that service, and lists the fixes to try in order, such as **Restart slurmd…**, then **Reboot…**.

{{< uishot "node" >}}

{{< /ui >}}
{{< /ways >}}

Shows the health of a single node. `NODE` is a short name, optionally followed by its cluster (`worker-0.dev`); the cluster defaults to `SIND_CLUSTER`, else `default`. A full DNS name ending in `.sind` is rejected.

```
CONTAINER            ROLE        FQDN                      IP          STATUS
sind-dev-controller  controller  controller.dev.sind.sind  172.19.0.2  running

SERVICES
NAME       STATUS
munge      ✓
slurmctld  ✓
sshd       ✓
```

Controllers of a backup pair get the same `HA` column as in `sind get cluster`.

## Logs

{{< ways >}}
{{< cli >}}

```bash
sind logs NODE [SERVICE] [--follow|-f] [--tail|-n N]
```

{{< /cli >}}
{{< ui >}}

Open the **Logs** tab of the node's page, or press **Logs** beside the node or one of its services anywhere in sind ui. It opens the log that tells the most: the journal of a service that failed, of the node's Slurm service while it runs, otherwise its container log. The **Logs** tab of a cluster shows the log of any of its nodes: select the node, and its container log or the journal of one of its services. The tab starts with the last 200 lines and follows the log; **Start with** shows more. Use **Pause** to stop the stream and **Follow** to resume it. The log of a stopped node ends where it stopped, and the tab says when. **Filter** and **Errors and warnings only** narrow the lines, and **Download** saves them.

{{< uishot "node-logs" >}}

{{< /ui >}}
{{< /ways >}}

Without a `SERVICE` argument, shows container logs (stdout/stderr). With a service name, shows journalctl output for that systemd unit.

```bash
# Container logs
sind logs controller
sind logs controller --follow
sind logs controller --tail 50        # only the last 50 lines

# Service logs
sind logs controller slurmctld
sind logs worker-0 slurmd --follow
sind logs submitter sackd             # identity clientIds
sind logs db slurmdbd
sind logs db mariadb
sind logs api slurmrestd
```

## List nodes

{{< ways >}}
{{< cli >}}

```bash
sind get nodes [CLUSTER]
```

{{< /cli >}}
{{< ui >}}

Open **Nodes** in the rail for every node of the realm, or the **Nodes** tab of a cluster for its nodes.

{{< uishot "nodes" >}}

{{< /ui >}}
{{< /ways >}}

Without a `CLUSTER` argument, lists every node in the realm with an extra `CLUSTER` column; with a cluster name, scopes to that cluster and drops the column. Rows are sorted by `(cluster, role, natural-name)` so `worker-2` precedes `worker-10`.

```
CONTAINER                CLUSTER  ROLE        FQDN                          IP          STATUS
sind-default-controller  default  controller  controller.default.sind.sind  172.19.0.2  running
sind-default-worker-0    default  worker      worker-0.default.sind.sind    172.19.0.3  running
sind-default-worker-1    default  worker      worker-1.default.sind.sind    172.19.0.4  running
```

## List networks

{{< ways >}}
{{< cli >}}

```bash
sind get networks
```

{{< /cli >}}
{{< ui >}}

Open the cluster, then the **Network and storage** tab. **Mesh and DNS** shows the mesh network.

{{< uishot "cluster-network" >}}

{{< /ui >}}
{{< /ways >}}

```
NAME              DRIVER  SUBNET         GATEWAY
sind-default-net  bridge  172.19.0.0/16  172.19.0.1
sind-mesh         bridge  172.18.0.0/16  172.18.0.1
```

## List realms

{{< ways >}}
{{< cli >}}

```bash
sind get realms
```

{{< /cli >}}
{{< ui >}}

The **Clusters** table of the Overview has a row for each realm with its clusters, nodes and health, and the rows of its clusters under it. The realm switcher at the head of the **Realm** group of the rail selects the realm that Clusters, Nodes, Mesh and DNS, Monitoring and Topology show.

{{< uishot "overview" >}}

{{< /ui >}}
{{< /ways >}}

```
NAME  CLUSTERS
ci    1
sind  2
```

Lists all active realms discovered from Docker container labels. Useful when working with multiple realms to see what's running.

## List volumes

{{< ways >}}
{{< cli >}}

```bash
sind get volumes
```

{{< /cli >}}
{{< ui >}}

Open the cluster, then the **Network and storage** tab. It lists the volumes and mounts of the cluster, each as a Docker volume or a host directory, read-only or read-write, and where the munge key is.

{{< uishot "cluster-network" >}}

{{< /ui >}}
{{< /ways >}}

```
NAME                 DRIVER
sind-default-config  local
sind-default-data    local
sind-default-munge   local
sind-ssh-config      local
```

## DNS records

{{< ways >}}
{{< cli >}}

```bash
sind get dns
```

{{< /cli >}}
{{< ui >}}

Open **Mesh and DNS**. It lists the DNS records of the realm. Type into the filter to find a name or an address; **Copy list** copies the records that show.

{{< uishot "mesh" >}}

{{< /ui >}}
{{< /ways >}}

```
HOSTNAME                      IP
controller.default.sind.sind  172.19.0.2
worker-0.default.sind.sind    172.19.0.3
```

## Authentication key

```bash
sind get auth-key [CLUSTER] [--type munge|slurm|jwt]
```

Outputs a key that authenticates the cluster's Slurm traffic, encoded as base64, suitable for injection into external tooling. `--type` picks the Slurm authentication plugin:

| `--type` | Key | Cluster |
|----------|-----|---------|
| `munge` | the munge key | identity `local` or `nssSlurm`, and every unmanaged cluster |
| `slurm` | `slurm.key` | [identity `clientIds`]({{< relref "/guides/users#clientids" >}}) |
| `jwt` | `jwt_hs256.key`, which signs [REST API tokens]({{< relref "/guides/rest-api" >}}) | with a managed [api node]({{< relref "/configuration/node-definitions#api-node" >}}) |

Without `--type` it prints the cluster's main key: `slurm.key` with identity `clientIds`, the munge key otherwise. A type the cluster does not have fails with an error that says why. `-o json` returns the key with its type, e.g. `{"type": "jwt", "key": "..."}`. `sind get auth-key` replaces `sind get munge-key`, which v0.11.0 removed: `sind get auth-key --type munge` prints the same key.

## Mesh infrastructure

{{< ways >}}
{{< cli >}}

```bash
sind get mesh
```

{{< /cli >}}
{{< ui >}}

Open **Mesh and DNS**. It shows the mesh network, the DNS and SSH containers, and the containers that are connected to the realm.

{{< uishot "mesh" >}}

{{< /ui >}}
{{< /ways >}}

Shows the realm's mesh infrastructure: network name, DNS container/IP/zone/image, and the SSH container/volume/image. Useful for external consumers that need to connect to sind networks without reimplementing the naming conventions. The DNS IP is pinned for as long as the mesh exists (see [Networking]({{< relref "/architecture/networking#after-a-host-reboot-or-a-docker-daemon-restart" >}})), so a DNS server outside sind, such as a [kind cluster]({{< relref "/guides/kind" >}})'s CoreDNS, can forward the `<realm>.sind` zone to it; it is `-` while the DNS container is stopped (`""` with `-o json`).

If no mesh has been created yet, the command returns a friendly `no mesh found for realm "<name>"` error.

## CoreDNS stub zone

```bash
sind get coredns-config
```

Prints a CoreDNS server block that forwards the realm's zone to the mesh DNS server, for a DNS server outside sind, such as the CoreDNS of a [kind cluster]({{< relref "/guides/kind" >}}):

```
sind.sind:53 {
    errors
    cache 30
    forward . 172.18.255.254
}
```

`-o json` returns the zone, the server address and the block, e.g. `{"zone": "sind.sind", "server": "172.18.255.254", "corefile": "..."}`. The command fails while the DNS container is stopped, as it then has no address.

## Monitoring stack

{{< ways >}}
{{< cli >}}

```bash
sind get monitoring
```

{{< /cli >}}
{{< ui >}}

Open **Monitoring**. It shows the clusters that the stack collects, with their key numbers from Prometheus, and Grafana, Prometheus, Loki and Alloy with their addresses and states.

{{< uishot "monitoring" >}}

{{< /ui >}}
{{< /ways >}}

Shows the realm's [monitoring stack]({{< relref "/guides/monitoring" >}}): each service's container, address and state, and the clusters it collects, with their number of monitored nodes and their Slurm version.

```
SERVICE     CONTAINER        ADDRESS                           STATUS
grafana     sind-grafana     http://grafana.sind.sind:3000     running
prometheus  sind-prometheus  http://prometheus.sind.sind:9090  running
loki        sind-loki        http://loki.sind.sind:3100        running
alloy       sind-alloy       http://172.18.0.9:12345           running

CLUSTERS
NAME     NODES  SLURM
default  3      26.05.5
dev      5      26.05.5
```

`ADDRESS` uses the services' DNS names, which resolve on the host with [host DNS]({{< relref "/architecture/networking#host-dns-resolution" >}}), and Alloy's address, as Alloy has none; it shows `-` for a stopped Alloy. `STATUS` is the container's state, `not ready` for a running container whose readiness check fails, `unchecked` for a running container while Grafana, which runs the checks, does not, or `missing` for a container that does not exist, as is its `status` in the JSON output. `-o json` adds each service's image, address (`ip`) readiness (`ready`) and whether it was checked (`checked`), and each cluster's key numbers from Prometheus under `slurm`: Slurm's nodes, those down and those draining, and the running and pending jobs. `slurm` is left out while Prometheus has no numbers of the cluster, such as just after the create:

```json
{
  "realm": "sind",
  "services": [
    {"name": "grafana", "container": "sind-grafana", "image": "grafana/grafana:13.2.4",
     "ip": "172.18.255.253", "url": "http://grafana.sind.sind:3000", "status": "running", "ready": true, "checked": true}
  ],
  "clusters": [{"name": "default", "nodes": 3, "slurm_version": "26.05.5",
                "slurm": {"nodes": 1, "nodes_down": 0, "nodes_drain": 0, "jobs_running": 2, "jobs_pending": 5}}]
}
```

In a realm without a stack, the command fails with `no monitoring stack in realm "<name>"` and exit status 1.

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
