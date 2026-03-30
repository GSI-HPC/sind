---
weight: 120
title: "Quickstart"
icon: "play_arrow"
description: "Create your first Slurm cluster in minutes"
toc: true
---

<!-- Keep in sync with TestQuickstart in cmd/sind/quickstart_test.go -->

## Create a cluster

The simplest command creates a cluster named `default` with one controller and one worker:

```bash
sind create cluster
```

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

## Check cluster status

```bash
sind get clusters
```

```
NAME      NODES (S/C/W)   SLURM    STATUS
default   2 (0/1/1)       25.11    running
```

View individual nodes:

```bash
sind get nodes
```

```
NAME                 ROLE         STATUS
controller.default   controller   running
worker-0.default     worker       running
```

For detailed health information:

```bash
sind status
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
EOF
```

Submit and monitor:

```bash
sind exec -- sbatch job.sh
```

```
Submitted batch job 2
```

Check the job queue:

```bash
sind exec -- squeue
```

View the output after the job completes:

```bash
cat slurm-2.out
```

```
Hello from worker-0
```

By default, `sind create cluster` bind-mounts the current directory as `/data` inside all nodes. Batch scripts and output files are shared across the cluster and accessible directly on your host.

## SSH into a node

```bash
sind ssh controller
sind ssh worker-0
```

## Create a larger cluster

Use a configuration file for more complex setups:

```yaml
kind: Cluster
name: dev
defaults:
  cpus: 4
  memory: 8g
nodes:
  - controller
  - submitter
  - worker: 5
```

```bash
sind create cluster --config cluster.yaml
```

## Add workers dynamically

```bash
sind create worker --count 3
```

## Tear down

Delete a specific cluster:

```bash
sind delete cluster default
```

Or delete all clusters:

```bash
sind delete cluster --all
```

When the last cluster is deleted, sind automatically cleans up the shared mesh infrastructure.
