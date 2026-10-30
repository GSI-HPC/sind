---
weight: 310
title: "Cluster Lifecycle"
description: "Creating, listing, and deleting clusters"
toc: true
---

## Create a cluster

{{< ways >}}
{{< cli >}}

```bash
sind create cluster [NAME] [--config FILE] [--pull] [--data PATH|volume] [--monitoring] [--wait DURATION]
```

{{< /cli >}}
{{< ui >}}

1. Press **New cluster…** on the Overview or on the Clusters page. The New cluster page opens.
2. Pick a start: **Quick test**, **With job history**, **Like a site**, or **Copy a cluster** for the nodes of a cluster you have. **Start from YAML…** takes a config that you paste or open from a file.
3. Set the name and the realm, and change the nodes, **Your files** or **More options** as you need. The plan at the right shows the nodes, what they take of this host, the command and the config as you type.
4. Press **Create** when the status at the bottom of the plan says `Ready`.

{{< uishot "new-cluster" >}}

sind ui saves the config and runs `sind create cluster --config FILE`, with `--pull` when **Pull the image** is on and `--wait` from **Wait for ready**. The data storage and monitoring go into the config. [Actions]({{< relref "/web-ui/actions#new-cluster" >}}) describes each part of the page.

{{< /ui >}}
{{< /ways >}}

| Argument/Flag | Default | Description |
|---------------|---------|-------------|
| `NAME` | `SIND_CLUSTER`, else the config's `name`, else `default` | Cluster name (positional, optional) |
| `--config` | — | Path to YAML configuration file, or `-` to read it from stdin; an empty value is a usage error |
| `--pull` | `false` | Pull each image of the cluster once before creating containers |
| `--data` | `.` | Host directory to mount at `/data` on all nodes, or `volume` to use a Docker volume; an empty value is a usage error. Ignored if the config sets `storage.dataStorage.type` or `hostPath`. |
| `--monitoring` | `false` | Collect the cluster's metrics and logs in the realm's [monitoring stack]({{< relref "/guides/monitoring" >}}); adds to the config's `monitoring`, which `--monitoring=false` does not turn off |
| `--wait` | `5m` | How long to wait for the nodes and Slurm to become ready, counted from when the node containers have started; `0` for no limit |

Without `--config`, sind creates a minimal cluster (1 controller + 1 worker) using the default image. `--config -` reads the configuration from stdin; empty input is then an error.

Reading a stdin that is not a terminal without `--config -` is deprecated and will be removed: sind still does it for now, but first prints a `Warning:`, and empty input creates the minimal cluster. A script that runs sind with a pipe it does not mean as the configuration, such as `ssh HOST sind create cluster` or a `while read` loop, should redirect stdin from `/dev/null`.

When both a positional `NAME` and a config file with a `name:` field are provided, the positional argument takes precedence. [`SIND_CLUSTER`]({{< relref "/usage/environment#the-cluster" >}}) comes between them: it names the cluster when `NAME` does not, whatever the config's `name:` says.

```bash
# Minimal cluster (named "default")
sind create cluster

# Named cluster
sind create cluster dev

# From config file
sind create cluster --config cluster.yaml

# Config file with name override
sind create cluster staging --config cluster.yaml

# Pipe config from stdin
sind create cluster --config - << 'EOF'
kind: Cluster
name: dev
nodes:
  - controller
  - worker: 3
EOF
```

### What happens during creation

1. Mesh infrastructure is created if not already present (network, DNS, SSH), or started if it is stopped
2. Cluster network and volumes are created
3. Munge key and Slurm configuration are generated
4. All node containers start in parallel
5. sind waits for each node to become ready (systemd, sshd, Slurm daemons)

On a terminal, sind shows these steps as it goes, and leaves a line for each one it finished (see [Progress Display]({{< relref "/usage/progress#what-the-commands-show" >}})).

`--wait` limits how long sind waits for the nodes, the Slurm daemons and, with `accounts`, for slurmdbd to register the cluster. Each node's limit counts from when its container has started, so pulling the image does not count against it: sind pulls the images it does not have, once each, before it creates the first node. A container that exits, or a munge, slurmctld, slurmd, sackd, slurmrestd or slurmdbd unit that fails, ends the wait at once with the tail of the unit's journal.

If a node is not ready within `--wait`, or a check fails for good, the command fails with exit status 1 and names the node and the last check that failed, for example:

```text
12:00:00.000 ERRO cluster "dev" not ready within 5m0s: node worker-0: timed out; last probe error: probe munge: munge not ready: activating
```

sind then removes the resources it created, including a mesh that this invocation set up and no other cluster uses, which a progress display shows as the step `rollback`. If that cleanup fails too, the error says so after the original one; use `sind delete cluster` to remove what is left. On a slow host or CI runner, raise the limit, e.g. `--wait 15m`, or use `--wait 0` to wait until interrupted.

Ctrl-C (SIGINT) or SIGTERM, as sent by `timeout` or `docker stop`, stops the creation and runs the same cleanup; sind then exits with status 130 (see [Exit Status]({{< relref "/usage/exit-status" >}})). A second signal ends sind at once, without waiting for the cleanup to finish.

### Preflight checks

Before it sets up the mesh or pulls an image, sind asks the Docker daemon whether it can start sind's nodes: a daemon in rootless mode or with `userns-remap` refuses their writable cgroups, one that runs containers on cgroup v1 cannot boot them, and a Docker Engine older than 28.0 refuses their options, so creation fails at once with an error that says so and points to `sind doctor` for the fix.

Before creating resources, sind checks for conflicts — containers, networks, or volumes with matching names that already exist. If conflicts are found, creation fails with an error. It also checks that the nodes fit on the realm's mesh and on the cluster network: a Docker bridge network holds at most 1,023 containers, and the mesh holds the nodes of every cluster in the realm (see [Limits]({{< relref "/architecture/networking#limits" >}})).

Before it creates a node container, sind checks that the Docker host mounts cgroup2 with the `nsdelegate` option, in a throwaway container of the controller's image, which shows the option of the kernel the daemon runs on. Without it, creation fails with the commands that enable it, the ones [`sind doctor`]({{< relref "/usage/diagnostics#doctor" >}}) prints, and removes what it has created so far. The check runs alongside the other preparation steps, so it costs no time when it passes.

## List clusters

{{< ways >}}
{{< cli >}}

```bash
sind get clusters
```

{{< /cli >}}
{{< ui >}}

Open **Clusters** in the rail. Each row is a cluster, with its state, its nodes and its Slurm release.

{{< uishot "clusters" >}}

{{< /ui >}}
{{< /ways >}}

```
NAME     NODES (S/C/D/A/W)  SLURM    STATUS
default  5 (1/1/0/1/2)      26.05.5  running
dev      4 (0/1/1/0/2)      25.11.8  running
```

The `NODES` column shows the total count and breakdown: **S**ubmitter / **C**ontroller / **D**b / **A**pi / **W**orker. `SLURM` shows `-` when sind does not know the version, as for unmanaged clusters.

## Delete a cluster

{{< ways >}}
{{< cli >}}

```bash
sind delete cluster [NAME]
```

{{< /cli >}}
{{< ui >}}

On the page of the cluster, press **Delete cluster…**, or on the **Clusters** page open the **⋯** menu of its row and press **Delete cluster…**. The dialog lists what goes, such as the containers, the network and the volumes, and what stays, such as the host directories the cluster mounts. Type the name of the cluster to confirm. When its data volume goes, tick the box that names it. Press **Delete cluster NAME**.

{{< uishot "clusters-row-menu" >}}

{{< uishot "dialog-delete-cluster" >}}

To delete several clusters, tick them on the **Clusters** page and press **Delete 2 clusters…** (it counts the clusters you ticked) in the bar above the table. The dialog has a row for each cluster, with what goes and what stays of it, and a row for a realm that goes with its last clusters. Type the number of clusters to confirm, tick the box that names the data volumes that go, if any, and press **Delete 2 clusters**. sind ui runs `sind delete cluster` once for each. To delete all clusters of a realm and its mesh, you can also delete the realm.

{{< uishot "clusters-selected" >}}

{{< uishot "dialog-delete-clusters" >}}

{{< /ui >}}
{{< /ways >}}

Deleting is idempotent — deleting a non-existent cluster is not an error. sind handles partial or broken clusters (e.g., from a failed creation).

Deletion order: stops/removes containers, disconnects/removes networks, removes volumes. Containers outside sind attached to its networks, such as [kind nodes]({{< relref "/guides/kind" >}}), are disconnected first, not removed. sind also updates `~/.local/state/sind/<realm>/known_hosts` (or `$XDG_STATE_HOME/sind/<realm>/known_hosts`) to remove deleted nodes.

```bash
# Delete the default cluster
sind delete cluster

# Delete a named cluster
sind delete cluster dev

# Delete all clusters
sind delete cluster --all
```

When the last cluster is deleted, sind also removes the shared mesh infrastructure (DNS, SSH, mesh network).

`--all` deletes every cluster of the realm in parallel, then the mesh, the monitoring stack and the exported SSH configuration, as [`sind delete realm`](#delete-a-realm) does. It finds the clusters by the labels of their containers, networks and volumes, and also removes a mesh that no cluster is left in, as after a create that was killed. A cluster that fails to delete does not stop the others: sind deletes the rest, keeps the mesh, and exits non-zero naming the failed cluster. So does a cluster whose deletion hits a bug in sind: its error says `sind panicked; this is a bug, please report it`, with the details on stderr, and the command exits 1. sind deletes at most four clusters at a time, and after Ctrl-C it starts no further one. On a terminal, the [progress display]({{< relref "/usage/progress#what-the-commands-show" >}}) shows them as the step `clusters`.

## Delete a realm

{{< ways >}}
{{< cli >}}

```bash
sind delete realm
sind --realm ci-42 delete realm
```

{{< /cli >}}
{{< ui >}}

On the Overview, open the **⋯** menu of the realm's row and press **Delete realm…**, or open **Mesh and DNS** and press **Delete realm…**. The dialog has a row for each cluster and one for the realm, with what goes, such as the mesh and the DNS server, and what stays, such as the containers outside sind. Type the name of the realm to confirm, tick the box that names the data volumes that go, if any, and press **Delete realm NAME**. The dialog runs `sind --realm NAME delete realm`, so a copy of the command deletes this realm in any shell.

{{< uishot "dialog-delete-realm" >}}

{{< /ui >}}
{{< /ways >}}

Deletes everything sind made for the realm, as `sind delete cluster --all` does: every cluster, the mesh, the [monitoring stack]({{< relref "/guides/monitoring" >}}) and the exported SSH configuration. The realm is the one `--realm`, `SIND_REALM` or the default names; `sind delete realm ci-42` is a usage error that points to `--realm`. External containers attached to the realm's networks are detached, not removed.

## Connect containers outside sind

{{< ways >}}
{{< cli >}}

```bash
sind connect container CONTAINER...
sind disconnect container CONTAINER...
```

{{< /cli >}}
{{< ui >}}

Press **Connect a container…** on **Mesh and DNS**, which shows the realm that the switcher of the rail picked, or in the **⋯** menu of a realm's row on the Overview. The dialog lists the containers that sind did not make, those that run first, with their image and state; pick one, or pick **Another name…** and type it. It connects the container to the realm in its title; with more than one realm, the buttons above the list switch it and keep the container you picked. It refuses a container that sind made, warns of a container that does not run or that sind ui does not see, and says which nodes a container that it sees then reaches. Press **Connect NAME**. The dialog connects one container. To detach a container, press **Disconnect…** next to it on **Mesh and DNS**.

{{< uishot "dialog-connect" >}}

{{< /ui >}}
{{< /ways >}}

`sind connect container` attaches containers that sind did not make, such as [kind nodes]({{< relref "/guides/kind" >}}), to the realm's mesh network and every cluster network, so that they resolve the realm's names through its DNS server and reach its nodes. Clusters created later are connected to them too, while the realm has a cluster: sind counts every container on the mesh that is not its own as connected. `sind disconnect container` detaches them from every network of the realm. Deleting a cluster or the realm detaches them as well, and never removes them. Both commands fail for a container that does not exist or that sind made. Deleting the realm's last cluster takes the mesh with it, so a cluster created after that is not connected to them: `sind delete cluster` then names them in a `Warning:` on stderr, and `sind connect container` attaches them again.
