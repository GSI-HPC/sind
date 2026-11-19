// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package cluster

import (
	"context"
	"testing"

	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConnectExternalLifecycle attaches a container outside sind to the
// realm, checks that a cluster network created afterwards gets it too, as
// Create connects it, and that Disconnect detaches it from both networks.
func TestConnectExternalLifecycle(t *testing.T) {
	t.Parallel()
	c, _ := testutil.NewClient(t)
	ctx := t.Context()
	meshMgr, realm := testMesh(ctx, t, c, "it-ext")
	ext := docker.ContainerName(realm + "-external")
	clusterNet := NetworkName(realm, "dev")
	t.Cleanup(func() {
		bg := context.Background()
		_ = c.KillContainer(bg, ext)
		_ = c.RemoveContainer(bg, ext)
		_ = c.RemoveNetwork(bg, clusterNet)
	})

	_, err := c.RunContainer(ctx, "--name", string(ext), "busybox:latest", "sleep", "120")
	require.NoError(t, err)
	require.NoError(t, Connect(ctx, c, meshMgr, ext))

	require.NoError(t, createClusterNetwork(ctx, c, realm, "dev"))
	connectExternal(ctx, c, meshMgr, clusterNet)
	info, err := c.InspectContainer(ctx, ext)
	require.NoError(t, err)
	assert.Contains(t, info.IPs, meshMgr.NetworkName())
	assert.Contains(t, info.IPs, clusterNet)

	require.NoError(t, Disconnect(ctx, c, meshMgr, ext))
	info, err = c.InspectContainer(ctx, ext)
	require.NoError(t, err)
	assert.NotContains(t, info.IPs, meshMgr.NetworkName())
	assert.NotContains(t, info.IPs, clusterNet)
}
