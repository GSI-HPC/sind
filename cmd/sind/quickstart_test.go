// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestQuickstart mirrors the quickstart guide step by step.
// Each section comment references the corresponding guide heading.
// Keep in sync with docs/content/getting-started/quickstart.md.
func TestQuickstart(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	c := realClient(t)
	checkPrerequisites(t, c)
	image := testImage(t)

	realm := "it-qs-" + testID

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()

	meshMgr := mesh.NewManager(c, realm)
	t.Cleanup(func() {
		bg := context.Background()
		names, _ := cluster.DiscoverClusterNames(bg, c, realm)
		for _, name := range names {
			_ = cluster.Delete(bg, c, meshMgr, name)
		}
		_ = meshMgr.CleanupMesh(bg)
	})

	// ## Create a cluster
	// sind create cluster
	cfgDir := t.TempDir()
	minimalCfg := filepath.Join(cfgDir, "minimal.yaml")
	require.NoError(t, os.WriteFile(minimalCfg, []byte("kind: Cluster\ndefaults:\n  image: "+image+"\n"), 0o644))

	_, stderr, err := executeWithRealmCtx(ctx, realm, "create", "cluster", "--config", minimalCfg, "--data", dataDir)
	require.NoError(t, err, "create cluster: stderr=%q", stderr)

	// ## Check cluster status
	// sind get clusters
	var stdout string
	stdout, _, err = executeWithRealmCtx(ctx, realm, "get", "clusters")
	require.NoError(t, err)
	assert.Contains(t, stdout, "default")
	assert.Contains(t, stdout, "running")

	// sind get nodes
	stdout, _, err = executeWithRealmCtx(ctx, realm, "get", "nodes")
	require.NoError(t, err)
	assert.Contains(t, stdout, "controller.default")
	assert.Contains(t, stdout, "worker-0.default")

	// sind get cluster
	stdout, _, err = executeWithRealmCtx(ctx, realm, "get", "cluster")
	require.NoError(t, err)
	assert.Contains(t, stdout, "CLUSTER")
	assert.Contains(t, stdout, "default")
	assert.Contains(t, stdout, "NODES")
	assert.Contains(t, stdout, "NETWORKS")
	assert.Contains(t, stdout, "MESH SERVICES")
	assert.Contains(t, stdout, "MOUNTS")

	// ## Run Slurm commands
	// sind exec -- sinfo
	stdout, stderr, err = executeWithRealmCtx(ctx, realm, "exec", "--", "sinfo")
	require.NoError(t, err, "exec sinfo: stdout=%q stderr=%q", stdout, stderr)
	assert.Contains(t, stdout, "PARTITION")

	// sind exec -- srun hostname
	stdout, stderr, err = executeWithRealmCtx(ctx, realm, "exec", "--", "srun", "hostname")
	require.NoError(t, err, "exec srun hostname: stdout=%q stderr=%q", stdout, stderr)
	assert.Contains(t, stdout, "worker-0")

	// ### Submit a batch job
	// Create batch script via exec (lands in /data = test temp dir)
	_, stderr, err = executeWithRealmCtx(ctx, realm, "exec", "--", "sh", "-c", `printf '#!/bin/sh\n#SBATCH --job-name=hello\nhostname\nsleep 2\n' > job.sh`)
	require.NoError(t, err, "create job script: stderr=%q", stderr)

	// sind exec -- sbatch job.sh
	stdout, stderr, err = executeWithRealmCtx(ctx, realm, "exec", "--", "sbatch", "job.sh")
	require.NoError(t, err, "sbatch: stdout=%q stderr=%q", stdout, stderr)
	assert.Contains(t, stdout, "Submitted batch job")

	// sind exec -- squeue (job should still be running thanks to sleep)
	stdout, _, err = executeWithRealmCtx(ctx, realm, "exec", "--", "squeue")
	require.NoError(t, err)
	assert.Contains(t, stdout, "hello")

	// Wait for job to finish, then verify output file
	_, stderr, err = executeWithRealmCtx(ctx, realm, "exec", "--", "sh", "-c", "while squeue -h -n hello | grep -q hello; do sleep 1; done")
	require.NoError(t, err, "wait for job: stderr=%q", stderr)

	stdout, _, err = executeWithRealmCtx(ctx, realm, "exec", "--", "sh", "-c", "cat slurm-*.out")
	require.NoError(t, err)
	assert.Contains(t, stdout, "worker-0")

	// ## SSH into a node
	// sind ssh controller
	stdout, stderr, err = executeWithRealmCtx(ctx, realm, "ssh", "controller", "--", "hostname")
	require.NoError(t, err, "ssh controller: stdout=%q stderr=%q", stdout, stderr)
	assert.Contains(t, stdout, "controller")

	// sind ssh worker-0
	stdout, stderr, err = executeWithRealmCtx(ctx, realm, "ssh", "worker-0", "--", "hostname")
	require.NoError(t, err, "ssh worker-0: stdout=%q stderr=%q", stdout, stderr)
	assert.Contains(t, stdout, "worker-0")

	// ## Scale up
	// sind create worker --count 3
	_, stderr, err = executeWithRealmCtx(ctx, realm, "create", "worker", "--count", "3")
	require.NoError(t, err, "create worker: stderr=%q", stderr)

	stdout, _, err = executeWithRealmCtx(ctx, realm, "get", "nodes")
	require.NoError(t, err)
	assert.Contains(t, stdout, "worker-1.default")
	assert.Contains(t, stdout, "worker-2.default")
	assert.Contains(t, stdout, "worker-3.default")

	// ## Tear down
	// sind delete cluster default
	_, _, err = executeWithRealmCtx(ctx, realm, "delete", "cluster", "default")
	require.NoError(t, err)

	// ## Going further — named clusters with custom configuration
	devYAML := "kind: Cluster\nname: dev\ndefaults:\n  image: " + image + "\n  cpus: 2\n  memory: 1g\nnodes:\n  - controller\n  - submitter\n  - worker: 3\n"

	_, stderr, err = executeWithRealmStdin(ctx, realm, devYAML, "create", "cluster", "--data", dataDir)
	require.NoError(t, err, "create dev cluster: stderr=%q", stderr)

	// Verify submitter is present and exec routes to it.
	stdout, stderr, err = executeWithRealmCtx(ctx, realm, "exec", "dev", "--", "hostname")
	require.NoError(t, err, "exec dev hostname: stdout=%q stderr=%q", stdout, stderr)
	assert.Contains(t, stdout, "submitter")

	// Cleanup: delete remaining cluster
	// sind delete cluster --all
	_, _, err = executeWithRealmCtx(ctx, realm, "delete", "cluster", "--all")
	require.NoError(t, err)

	// Verify everything is gone.
	exists, err := c.ContainerExists(ctx, docker.ContainerName(realm+"-dev-controller"))
	require.NoError(t, err)
	assert.False(t, exists)
}
