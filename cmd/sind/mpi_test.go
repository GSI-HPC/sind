// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package main

import (
	"context"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMPI creates a 3-worker cluster and runs a basic MPI program via srun.
func TestMPI(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	c := realClient(t)
	checkPrerequisites(t, c)
	image := testImage(t)

	realm := "it-mpi-" + testID

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()

	meshMgr := mesh.NewManager(c, realm)
	t.Cleanup(func() {
		bg := context.Background()
		for _, name := range []string{"controller", "worker-0", "worker-1", "worker-2"} {
			cn := docker.ContainerName(realm + "-default-" + name)
			_ = c.KillContainer(bg, cn)
			_ = c.RemoveContainer(bg, cn)
		}
		for _, vt := range []string{"config", "munge", "data"} {
			_ = c.RemoveVolume(bg, docker.VolumeName(realm+"-default-"+vt))
		}
		_ = c.RemoveNetwork(bg, docker.NetworkName(realm+"-default-net"))
		_ = meshMgr.CleanupMesh(bg)
	})

	// Create a cluster with 3 workers for multi-node MPI.
	cfg := "kind: Cluster\ndefaults:\n  image: " + image + "\nnodes:\n  - controller\n  - worker: 3\n"
	_, stderr, err := executeWithRealmStdin(ctx, realm, cfg, "create", "cluster", "--data", dataDir)
	require.NoError(t, err, "create cluster: stderr=%q", stderr)

	// Verify the MPI stack is functional via ompi_info.
	stdout, stderr, err := executeWithRealmCtx(ctx, realm, "exec", "--", "ompi_info", "--all")
	require.NoError(t, err, "ompi_info: stderr=%q", stderr)
	assert.Contains(t, stdout, "Open MPI")
	assert.Contains(t, stdout, "MCA pmix")
	assert.Contains(t, stdout, "MCA pml: ucx")
	t.Logf("ompi_info output:\n%s", stdout)

	// Compile a minimal MPI program with a global barrier inside the cluster.
	mpiSource := `
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
    printf("rank %d of %d on %s\\n", rank, size, hostname);
    MPI_Finalize();
    return 0;
}
`
	_, stderr, err = executeWithRealmCtx(ctx, realm, "exec", "--", "sh", "-c", "cat > hello_mpi.c << 'CSRC'\n"+mpiSource+"CSRC")
	require.NoError(t, err, "write source: stderr=%q", stderr)

	_, stderr, err = executeWithRealmCtx(ctx, realm, "exec", "--", "mpicc", "-o", "hello_mpi", "hello_mpi.c")
	require.NoError(t, err, "mpicc: stderr=%q", stderr)

	// Run across all 3 workers with one task per node.
	stdout, stderr, err = executeWithRealmCtx(ctx, realm, "exec", "--", "srun", "-N3", "--ntasks-per-node=1", "./hello_mpi")
	require.NoError(t, err, "srun mpi: stdout=%q stderr=%q", stdout, stderr)
	t.Logf("srun output:\n%s", stdout)

	// Verify all 3 ranks reported in.
	assert.Contains(t, stdout, "rank 0 of 3 on worker-")
	assert.Contains(t, stdout, "rank 1 of 3 on worker-")
	assert.Contains(t, stdout, "rank 2 of 3 on worker-")
}
