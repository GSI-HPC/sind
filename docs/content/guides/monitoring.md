---
weight: 359
title: "Monitoring"
description: "Collect Slurm's metrics, the nodes' resource use and their journals in Prometheus, Loki and Grafana"
toc: true
---

A monitored cluster sends its Slurm metrics, its nodes' resource use and their systemd journals to a monitoring stack that belongs to the realm: [Grafana Alloy](https://grafana.com/docs/alloy/) collects them, Prometheus stores the metrics, Loki the logs, and Grafana shows them on dashboards made for sind. The stack runs as four containers on the realm's mesh network and needs no agent in the nodes. Use it to watch a scheduler test, to find which node logged an error, or to see what a job really used next to what Slurm allocated.

## Turn it on

{{< ways >}}
{{< cli >}}

Set `monitoring: true` in the [cluster config]({{< relref "/configuration/cluster-config#monitoring" >}}):

```yaml
kind: Cluster
name: dev
monitoring: true
nodes: [controller, db, worker: 2]
```

```bash
sind create cluster --config cluster.yaml
```

Or add `--monitoring` to `sind create cluster`, also on top of a config:

```bash
sind create cluster --monitoring
```

{{< /cli >}}
{{< ui >}}

On the New cluster page of `sind ui`, open **Users, identity, monitoring** under **More options** and tick **Monitoring**. The page writes `monitoring: true` into the config. It needs Slurm 26.05 or later in the images, and the plan says so for an older image.

{{< uishot "new-cluster" >}}

{{< /ui >}}
{{< /ways >}}

The flag and the key are ORed: either turns monitoring on, and `--monitoring=false` does not turn off a config's `monitoring: true`.

The first monitored cluster of a realm creates the stack, and the next ones join it. `sind create cluster` returns once the stack is ready and knows the new nodes, within the same `--wait` limit as the nodes. Monitoring is fixed for the cluster's life: an existing cluster cannot be switched, and workers added with `sind create worker` are monitored when their cluster is.

## Requirements

- **Slurm 26.05 or later**, with the HTTP parser plugin: slurmctld serves its metrics, and slurmctld and slurmd their health endpoints, over HTTP on their RPC ports. The default image and the `26.05` images have it. A cluster on `sind-node:25.11` fails before any node exists:

  ```
  image ghcr.io/gsi-hpc/sind-node:25.11 cannot serve monitoring: Slurm 25.11.8 is older than 26.05, which monitoring needs
  ```

  Custom images need a few more things; see [Building Images]({{< relref "/container-images/building-images#with-monitoring" >}}).
- **A managed cluster.** An [unmanaged cluster]({{< relref "/guides/unmanaged-cluster" >}}) is rejected, as sind writes no `slurm.conf` for it. Unmanaged workers, db and api nodes of a managed cluster are collected, without Slurm probes.
- **Metrics without a token.** The [`main` section]({{< relref "/configuration/cluster-config#slurm-section" >}}) must not set a `MetricsType` other than `metrics/openmetrics`, `CommunicationParameters=disable_http` or `MetricsAuthUsers`, and with `PrivateData` it must also set `MetricsParameters=ignore_private_data`. sind sets `MetricsType=metrics/openmetrics` itself.

sind pulls the stack's four images with the node images when it creates the stack, so the first monitored create downloads them once; later ones reuse them.

## Open Grafana

Grafana listens on port 3000 of its container on the mesh. With [host DNS]({{< relref "/architecture/networking#host-dns-resolution" >}}), open:

{{< ways >}}
{{< cli >}}

```text
http://grafana.sind.sind:3000
```

{{< /cli >}}
{{< ui >}}

Open **Monitoring**. It shows the address of Grafana, Prometheus and Loki. Press **Open** next to one to open it.

{{< uishot "monitoring" >}}

{{< /ui >}}
{{< /ways >}}

The name is `grafana.<realm>.sind`, so a cluster in realm `ci-42` has its Grafana at `http://grafana.ci-42.sind:3000`. Without host DNS, use the address `sind get monitoring` prints in its JSON output; on Linux the host reaches the containers' addresses directly:

```bash
sind get monitoring -o json | jq -r '.services[] | select(.name == "grafana") | "http://\(.ip):3000"'
```

When Docker runs on another machine that you reach over SSH, forward the port:

```bash
ssh -L 3000:grafana.sind.sind:3000 devbox
```

and open `http://localhost:3000`. sind publishes no host port, so realms and CI jobs never compete for port 3000.

There is no login: every visitor is an anonymous org Admin, who can use Explore and edit dashboards and datasources. Grafana installs no plugins and contacts nothing on the internet, so the stack works offline once its images are on the host.

## What is collected

| Source | What | Where it comes from |
|--------|------|---------------------|
| Slurm metrics | Nodes, jobs, partitions and the scheduler as slurmctld counts them, and jobs per user and account | slurmctld's OpenMetrics endpoints (`/metrics/*`) on the controllers, every 30 s (users and accounts every 60 s) |
| Slurm health | Whether slurmctld is live and ready, slurmd ready, slurmdbd listening | HTTP probes of `/livez` and `/readyz`, and a TCP connect to slurmdbd, every 15 s |
| Resource use | CPU, memory, processes and disk I/O of each node's container, of each systemd unit in it and of each job | the nodes' cgroups on the host, every 15 s |
| Logs | Each node's whole journal: the Slurm daemons, MariaDB, systemd and every other unit | the node's persistent journal |

Every series and log line has the labels `realm`, `cluster`, `node` and `role`. Logs also have `unit` and `level`. The Slurm daemons and MariaDB log every line at journal priority info, so sind reads the level from the message: a slurmctld `error: ...` line has `level="err"`, a MariaDB `[Warning]` line `level="warning"`. Resource series have `unit` for a systemd unit's cgroup and `job_id` for a job's.

A standby slurmctld redirects the metrics to the controller in control, so on a cluster with a [backup controller]({{< relref "/guides/controller-failover" >}}) the scrape's `up` is 1 only on the controller in control.

## Dashboards

The dashboards are in the folder `sind`. Each one picks the realm and the cluster at the top.

| Dashboard | Shows |
|-----------|-------|
| Slurm cluster | Opens first: the controller in control, nodes and jobs by state, CPUs and memory allocated, partitions, and the scheduler's cycles and queues |
| Nodes | Each node's CPU, memory, processes and disk I/O next to what Slurm has allocated on it, and the use of each unit |
| Service health | The Slurm daemons' probes as a node by service matrix, which controller is in control, and the errors and warnings in the logs |
| Users and accounts | Running and pending jobs and allocated CPUs per user and per account, and the CPU and memory of each running job |
| Logs | The journals of the cluster's nodes, filtered by node, unit, level and a search text |

The node panels link to the Logs dashboard for the same node. The dashboards refresh every 30 s, and at most every 15 s, the shortest scrape interval. They cannot be changed in place: save a copy to change one. Explore queries Prometheus and Loki directly, for example the slurmctld errors of cluster `dev`:

```text
{cluster="dev", unit="slurmctld.service", level="err"}
```

Prometheus and Loki answer on the mesh too, at `prometheus.<realm>.sind:9090` and `loki.<realm>.sind:3100`, without authentication:

```bash
curl -s 'http://prometheus.sind.sind:9090/api/v1/query?query=slurm_nodes_idle'
```

## Show the stack

{{< ways >}}
{{< cli >}}

```bash
sind get monitoring
```

{{< /cli >}}
{{< ui >}}

Open **Monitoring**. It shows the clusters that the stack collects, with their Slurm nodes that take jobs and their running and pending jobs, and the services of the stack with their addresses and states. Press a button of **Read every** to set how often sind ui reads them.

{{< uishot "monitoring" >}}

{{< /ui >}}
{{< /ways >}}

```text
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

`STATUS` is the container's state, `not ready` while it runs but does not answer its readiness check, `unchecked` while it runs but Grafana, which runs the checks, does not, or `missing`. Without a stack, the command fails with `no monitoring stack in realm "sind"`. `sind get cluster` lists the stack's containers under `MESH SERVICES` and the journal mount under `MOUNTS` (see [Diagnostics]({{< relref "/usage/diagnostics#monitoring-stack" >}})).

## Journals

On a monitored cluster, each node keeps its journal on a Docker volume of the realm, `<realm>-journal`, in a directory of its own, `<cluster>/<node>`, mounted at `/var/log/journal`; Alloy reads it from there. sind caps it at 64 MB per node. The journal then survives `sind power reboot` and `cycle`, so `sind logs NODE SERVICE` also shows the node's earlier runs. A cluster created again under the same name, or a worker added again, starts with an empty journal.

MariaDB's own messages reach the journal only when its error log goes there, as in the official images. Unmanaged nodes are collected too: a cap that your provisioning sets for journald is replaced by sind's 64 MB.

## Lifecycle

- **Several clusters** of a realm share one stack, and its dashboards switch between them. A cluster created without monitoring in a realm with a stack is not collected.
- **`sind create worker`** adds the new workers to the stack, and **`sind delete worker`** and **`sind delete cluster`** take their nodes out. The metrics and logs collected from them stay until they expire.
- **Deleting the realm's last cluster** removes the stack with the mesh, its data included. The stack stays as long as the realm has a cluster, monitored or not.
- **After a host reboot** the stack is stopped like the nodes; `sind power on` starts it again (see [Power Control]({{< relref "/usage/power-control#power-on" >}})).
- **After a sind upgrade** that changes the stack's images, configuration or dashboards, the next monitored `sind create cluster` or `sind create worker` replaces the stack's containers and keeps their data.
- **Retention**: Prometheus keeps 7 days of metrics, at most 2 GB, and Loki 7 days of logs.

## In CI

Once the images are on the runner, the stack starts beside the Slurm daemons, and it needs no host port, so parallel jobs in their own realms do not collide. With [sind-action](https://github.com/GSI-HPC/sind-action), turn it on in the cluster config:

```yaml
- uses: GSI-HPC/sind-action@v2
  with:
    clusters: |
      - kind: Cluster
        monitoring: true
        nodes: [controller, worker: 2]
```

A later step can query the stack, or save a cluster's logs when the tests failed:

```bash
LOKI=http://$(sind get monitoring -o json | jq -r '.services[] | select(.name == "loki") | .ip'):3100
curl -sG "$LOKI/loki/api/v1/query_range" \
  --data-urlencode 'query={cluster="default", level=~"crit|err"}' \
  --data-urlencode limit=5000 \
  --data-urlencode "start=$(date -d '-1 hour' +%s)000000000" > slurm-errors.json
```

The stack's images come from Docker Hub; a runner that cannot reach it needs them preloaded (see [Installation]({{< relref "/getting-started/installation#container-image" >}})).

## Limits

- Anyone who reaches the mesh, local users of the host and jobs on the nodes alike, is a Grafana org Admin and can add datasources that point anywhere the Grafana container reaches. Prometheus and Loki have no authentication either. The stack is a development tool.
- Grafana, Prometheus and Loki get 1 GB of memory each, Alloy 512 MB.
- Slurm reports memory in MiB, as `slurm.conf` states `RealMemory`, whatever the `_bytes` in its metric names says.
- Job history, such as efficiency and timelines as `sacct` shows them, is not collected: no metric carries accounting data.
