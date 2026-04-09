---
weight: 351
title: "Running MPI Jobs"
icon: "hub"
description: "Compile and run multi-node MPI programs with OpenMPI and PMIx"
toc: true
---

The default sind-node image ships with a full MPI stack (OpenMPI, PMIx, PRRTE, UCX) and Slurm configured with `MpiDefault=pmix`. This guide walks through compiling and running a simple MPI program across multiple nodes.

## Create a multi-node cluster

Create a cluster with 3 workers:

```bash
sind create cluster <<'EOF'
kind: Cluster
nodes:
  - controller
  - worker: 3
EOF
```

## Verify the MPI stack

Check that OpenMPI is installed and configured with PMIx and UCX:

```bash
sind exec -- ompi_info | grep -E "Open MPI:|MCA pml|with-pmix"
```

You should see OpenMPI's version, the UCX point-to-point layer, and `--with-pmix` in the configure line.

## Write an MPI program

Create a hello-world program that synchronizes all ranks with a barrier:

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

## Compile

The image includes `mpicc`, which wraps gcc with the correct MPI flags:

```bash
sind exec -- mpicc -o hello_mpi hello_mpi.c
```

## Run across all nodes

Launch one task per worker node via `srun`:

```bash
sind exec -- srun -N3 --ntasks-per-node=1 ./hello_mpi
```

Expected output (order may vary):

```
rank 0 of 3 on worker-0
rank 1 of 3 on worker-1
rank 2 of 3 on worker-2
```

Since `MpiDefault=pmix` is set in the generated `slurm.conf`, `srun` uses PMIx for process launch automatically — no `--mpi=pmix` flag needed.

## Batch submission

The same program works with `sbatch`:

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
