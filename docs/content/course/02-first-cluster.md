---
weight: 72
title: "02 · Your first cluster"
description: "Install, doctor, create, submit, ssh in, delete"
---

{{< video "course-02-first-cluster" >}}

You have a Linux machine with Docker and a batch script to try. This episode installs sind, checks the host, creates a cluster, runs the script on it, logs in to a node and deletes the cluster again.

## In this episode

- Download, verify and install the sind binary: [Installation]({{< relref "/getting-started/installation" >}})
- Check the host with `sind doctor`: [Diagnostics]({{< relref "/usage/diagnostics#doctor" >}})
- Create a cluster, list it and its nodes, and run a batch job from your directory: [Quickstart]({{< relref "/getting-started/quickstart" >}})
- SSH into a node: [Node Access]({{< relref "/usage/node-access#ssh" >}})
- Delete the cluster, and with the last one the shared mesh: [Cluster Lifecycle]({{< relref "/usage/cluster-lifecycle#delete-a-cluster" >}})

## Commands

The episode runs these commands in one session on a Linux amd64 machine, in a working directory that becomes `/data` on every node:

```bash
curl -fLO https://github.com/GSI-HPC/sind/releases/latest/download/sind-linux-amd64
gh attestation verify sind-linux-amd64 --repo GSI-HPC/sind \
  --signer-workflow GSI-HPC/sind/.github/workflows/release.yml
```

```bash
install -D ./sind-linux-amd64 ~/.local/bin/sind
```

```bash
sind doctor
```

```text
✓ Docker Engine: 28.1.1 (>= 28.0)
✓ Docker daemon: rootful, no userns-remap
✓ Docker host: this machine (unix:///var/run/docker.sock)
✓ cgroupv2: nsdelegate enabled (/sys/fs/cgroup)
✓ inotify: max_user_instances 8192 (>= 1024)
✓ DNS policy: host resolution available
```

```bash
sind create cluster
```

```bash
sind get clusters
```

```text
NAME      NODES (S/C/D/A/W)   SLURM     STATUS
default   2 (0/1/0/0/1)       26.05.4   running
```

```bash
sind get nodes
```

```text
CONTAINER                CLUSTER   ROLE         FQDN                            IP           STATUS
sind-default-controller  default   controller   controller.default.sind.sind    172.19.0.2   running
sind-default-worker-0    default   worker       worker-0.default.sind.sind      172.19.0.3   running
```

```bash
cat > job.sh << 'EOF'
#!/bin/bash
#SBATCH --job-name=hello
echo "Hello from $(hostname)"
sleep 30
EOF
```

```bash
sind exec -- sbatch job.sh
```

```text
Submitted batch job <JOBID>
```

```bash
cat slurm-<JOBID>.out
```

```text
Hello from worker-0
```

```bash
sind ssh worker-0
```

```bash
sind delete cluster default
```

## Go deeper

- [Installation]({{< relref "/getting-started/installation" >}}): mise, building from source, a system-wide install and the node image
- [Diagnostics]({{< relref "/usage/diagnostics" >}}): what each `sind doctor` check means, and how to fix a failed one
- [Quickstart]({{< relref "/getting-started/quickstart" >}}): the same path on one page, plus scaling up and named clusters
- [Node Access]({{< relref "/usage/node-access" >}}): `sind enter`, `sind exec`, the data mount and your own `ssh` client
- [Cluster Lifecycle]({{< relref "/usage/cluster-lifecycle" >}}): named clusters, config files, `--wait` and deleting every cluster
- Installation, Diagnostics, Node Access and Cluster Lifecycle each open with a short clip
