// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package cluster

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClusterCreateDeleteLifecycle(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	ctx := t.Context()

	skipIfNoNsdelegate(t)

	img := os.Getenv("SIND_TEST_IMAGE")
	if img == "" {
		img = "ghcr.io/gsi-hpc/sind-node:latest"
	}

	realm := testutil.Realm("it-cluster")
	clusterName := "it-cluster"
	meshMgr := mesh.NewManager(c, realm)

	t.Cleanup(func() {
		bg := context.Background()
		_ = Delete(bg, c, meshMgr, clusterName)
		_ = meshMgr.CleanupMesh(bg)
	})

	err := meshMgr.EnsureMesh(ctx)
	require.NoError(t, err)

	cfg, err := config.Parse([]byte(fmt.Sprintf(`
kind: Cluster
name: %s
defaults:
  image: %s
`, clusterName, img)))
	require.NoError(t, err)
	cfg.ApplyDefaults()
	require.NoError(t, cfg.Validate())

	// Create.
	result, err := Create(ctx, c, meshMgr, cfg, 500*time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, clusterName, result.Name)
	assert.Equal(t, StateRunning, result.State)
	require.Len(t, result.Nodes, 2)

	// GetClusters.
	clusters, err := GetClusters(ctx, c, realm)
	require.NoError(t, err)
	var found bool
	for _, cl := range clusters {
		if cl.Name == clusterName {
			found = true
			assert.Equal(t, 2, cl.NodeCount)
		}
	}
	assert.True(t, found, "cluster should appear in GetClusters")

	// GetNodes.
	nodes, err := GetNodes(ctx, c, realm, clusterName)
	require.NoError(t, err)
	assert.Len(t, nodes, 2)

	// Delete.
	err = Delete(ctx, c, meshMgr, clusterName)
	require.NoError(t, err)

	// Verify gone.
	clusters, err = GetClusters(ctx, c, realm)
	require.NoError(t, err)
	for _, cl := range clusters {
		assert.NotEqual(t, clusterName, cl.Name)
	}

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func skipIfNoNsdelegate(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		t.Skip("cannot read /proc/mounts")
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "cgroup2") && strings.Contains(line, "nsdelegate") {
			return
		}
	}
	t.Skip("host cgroup mount lacks nsdelegate (mount -o remount,nsdelegate /sys/fs/cgroup)")
}
