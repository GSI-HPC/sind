---
weight: 74
title: "04 · Test your job scripts before the queue"
description: "The data directory, sbatch and srun, MPI across workers"
---

{{< video "course-04-job-scripts" >}}

Your job script waits hours in the production queue and then fails on a typo. With sind and Docker, you test it on your laptop first: this episode creates a cluster in your project directory, submits a batch job from it, and runs an MPI program across three workers.

## In this episode

- Create a cluster with three workers from a config on stdin, as in [Cluster Lifecycle]({{< relref "/usage/cluster-lifecycle" >}}).
- Find your working directory on every node at `/data`, the [data mount]({{< relref "/usage/node-access#data-mount" >}}) that `sind exec` and `sind enter` start in.
- Submit a batch job and read its output on your laptop, as in the [Quickstart]({{< relref "/getting-started/quickstart#submit-a-batch-job" >}}).
- Compile an MPI program and run one rank per worker with `srun`, from [Running MPI Jobs]({{< relref "/guides/mpi-jobs" >}}).

## Commands

The commands and output from the video, in order, run in your project directory. First, a cluster named `default` with one controller and three workers:

```bash
sind create cluster --config - <<'EOF'
kind: Cluster
nodes:
  - controller
  - worker: 3
EOF
```

`sind get cluster` lists the data mount under `MOUNTS` (an excerpt of its output):

```bash
sind get cluster
```

```text
MOUNTS
MOUNT        SOURCE               TYPE       STATUS
/data        /home/user/project   hostPath   ✓
```

A batch script, written on your laptop and submitted from the cluster:

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

Once the job has finished, its output file is in your project directory:

```bash
cat slurm-<JOBID>.out
```

```text
Hello from worker-0
```

The MPI program, which `sind exec` writes into the data directory:

```bash
sind exec -- sh -c 'cat > hello_mpi.c << "CSRC"
#include <mpi.h>
#include <stdio.h>
#include <unistd.h>

int main(int argc, char **argv) {
    MPI_Init(&argc, &argv);

    int rank, size;
    char hostname[256];
    MPI_Comm_rank(MPI_COMM_WORLD, &rank);
    MPI_Comm_size(MPI_COMM_WORLD, &size);
    gethostname(hostname, sizeof(hostname));

    MPI_Barrier(MPI_COMM_WORLD);
    printf("rank %d of %d on %s\n", rank, size, hostname);

    MPI_Finalize();
    return 0;
}
CSRC'
```

Compile it, and run one task on each of the three workers:

```bash
sind exec -- mpicc -o hello_mpi hello_mpi.c
sind exec -- srun -N3 --ntasks-per-node=1 ./hello_mpi
```

```text
rank 0 of 3 on worker-0
rank 1 of 3 on worker-1
rank 2 of 3 on worker-2
```

On the real cluster, `srun` runs inside a job script. The MPI guide's script, and how it submits it:

```bash
sind exec -- sh -c 'cat > mpi_job.sh << "SCRIPT"
#!/bin/sh
#SBATCH --job-name=mpi-hello
#SBATCH --nodes=3
#SBATCH --ntasks-per-node=1
srun hello_mpi
SCRIPT'

sind exec -- sbatch --wait mpi_job.sh
sind exec -- sh -c 'cat slurm-*.out'
```

Delete the cluster when you are done:

```bash
sind delete cluster
```

## Refresher: MPI ranks and srun

What an MPI rank is, and how `srun` starts one task per rank across the allocated nodes: [the full refresher]({{< relref "refreshers#mpi-ranks-and-srun" >}}).

## Go deeper

- [Node Access]({{< relref "/usage/node-access" >}}): `sind exec`, `sind enter`, and the `--data` options of the data mount.
- [Running MPI Jobs]({{< relref "/guides/mpi-jobs" >}}): the MPI stack, and the same program as a batch job, with a clip.
- [Using CVMFS]({{< relref "/guides/cvmfs" >}}): software from `/cvmfs` on every node, with a clip.
- [Cluster Lifecycle]({{< relref "/usage/cluster-lifecycle" >}}): what `sind create cluster` does, and options such as `--wait`.
- [Diagnostics]({{< relref "/usage/diagnostics#cluster-status" >}}): the full output of `sind get cluster`.
