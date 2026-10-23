---
weight: 120
title: "Quickstart"
description: "Create your first Slurm cluster in minutes"
toc: true
---

<!-- Keep in sync with TestQuickstart in cmd/sind/quickstart_test.go -->

## Create a cluster

sind mounts the working directory read-write into every node, and the Slurm jobs write their output there as root. Work in a scratch directory, not in your home directory:

```bash
mkdir sind-demo && cd sind-demo
```

The simplest command creates a cluster named `default` with one controller and one worker:

{{< ways >}}
{{< cli >}}

```bash
sind create cluster
```

{{< /cli >}}
{{< ui >}}

1. Start the web UI. It opens in your browser:

   ```bash
   sind ui
   ```

2. Press **New cluster…**. The dialog proposes a free name, such as `hpc`, with a controller, a db node, a submitter and two workers. Set the name to `default`, clear **db** and **submitter**, and set **workers** to 1.
3. Press **Create** when the status at the bottom says `Ready`.

{{< uishot "dialog-create-cluster" >}}

The **Runs** line of the dialog shows the command that it starts. The dialog also saves the config of the cluster, so you can run the same command from a terminal.

{{< /ui >}}
{{< /ways >}}

This is equivalent to using this configuration:

```yaml
kind: Cluster
name: default
nodes:
  - role: controller
  - role: worker
```

sind will:

1. Set up mesh infrastructure (network, DNS, SSH) if not already present
2. Create a cluster network and volumes
3. Generate munge keys and Slurm configuration
4. Start all node containers
5. Wait for all nodes to become ready

On a terminal, sind shows its progress while it works and leaves a line for each step it finished; where standard error is not a terminal, it prints nothing on success. See [Progress Display]({{< relref "/usage/progress" >}}).

## Check cluster status

{{< ways >}}
{{< cli >}}

```bash
sind get clusters
```

{{< /cli >}}
{{< ui >}}

Open **Clusters** in the rail, then the cluster. The **Summary** tab shows each node and its services, such as munge, sshd and slurmctld.

{{< uishot "cluster" >}}

The rest of this section shows the same with `sind get`.

{{< /ui >}}
{{< /ways >}}

```
NAME     NODES (S/C/D/A/W)  SLURM    STATUS
default  2 (0/1/0/0/1)      26.05.5  running
```

View individual nodes:

```bash
sind get nodes
```

```
CONTAINER                CLUSTER  ROLE        FQDN                          IP          STATUS
sind-default-controller  default  controller  controller.default.sind.sind  172.19.0.2  running
sind-default-worker-0    default  worker      worker-0.default.sind.sind    172.19.0.3  running
```

For detailed health information:

```bash
sind get cluster
```

## Run Slurm commands

Open an interactive shell on the controller (or submitter, if configured):

```bash
sind enter
```

Or run one-shot commands without an interactive session:

```bash
sind exec -- sinfo
sind exec -- srun hostname
```

### Submit a batch job

Create a batch script in your working directory:

```bash
cat > job.sh << 'EOF'
#!/bin/bash
#SBATCH --job-name=hello
echo "Hello from $(hostname)"
sleep 30
EOF
```

Submit and monitor:

```bash
sind exec -- sbatch job.sh
```

```
Submitted batch job <JOBID>
```

Check the job queue:

```bash
sind exec -- squeue
```

View the output after the job completes:

```bash
cat slurm-<JOBID>.out
```

```
Hello from worker-0
```

By default, `sind create cluster` bind-mounts the current directory as `/data` inside all nodes. Batch scripts and output files are shared across the cluster and accessible directly on your host.

## SSH into a node

{{< ways >}}
{{< cli >}}

```bash
sind ssh controller
sind ssh worker-0
```

{{< /cli >}}
{{< ui >}}

The web UI has no terminal. Open the cluster, then the **Connect** tab, and press **Copy** next to the command for the node. Run it in your terminal.

{{< uishot "cluster-connect" >}}

{{< /ui >}}
{{< /ways >}}

## Scale up

Add more workers to the running cluster:

{{< ways >}}
{{< cli >}}

```bash
sind create worker --count 3
```

{{< /cli >}}
{{< ui >}}

On the page of the cluster, press **Add workers…**, set **How many** to 3, and press **Add**.

{{< uishot "dialog-create-worker" >}}

{{< /ui >}}
{{< /ways >}}

## Tear down

Delete the cluster:

{{< ways >}}
{{< cli >}}

```bash
sind delete cluster default
```

{{< /cli >}}
{{< ui >}}

On the page of the cluster, press **Delete cluster…**. Type the name of the cluster to confirm, and press **Delete cluster default**.

{{< uishot "dialog-delete-cluster" >}}

{{< /ui >}}
{{< /ways >}}

Or delete all clusters at once:

```bash
sind delete cluster --all
```

When the last cluster is deleted, sind automatically cleans up the shared mesh infrastructure.

## Going further

### Named clusters with custom configuration

Pipe a configuration directly into `sind create cluster --config -`:

```bash
sind create cluster --config - << 'EOF'
kind: Cluster
name: dev
defaults:
  cpus: 2
  memory: 1g
nodes:
  - controller
  - submitter
  - worker: 3
EOF
```

Or use a configuration file:

```bash
sind create cluster --config cluster.yaml
```

Multiple clusters can run side by side — each gets its own network, volumes, and DNS namespace. See [Cluster Configuration](../../configuration/cluster-config/) for all available options.
