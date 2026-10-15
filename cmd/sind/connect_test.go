// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"testing"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectContainer_RequiresArgs(t *testing.T) {
	_, _, err := executeCommand("connect", "container")
	assert.Error(t, err)
}

func TestDisconnectContainer_RequiresArgs(t *testing.T) {
	_, _, err := executeCommand("disconnect", "container")
	assert.Error(t, err)
}

// --- Integration ---

// TestConnectContainerLifecycle attaches a container outside sind to the
// realm, checks that a cluster network created afterwards gets it too,
// detaches it, and deletes the realm.
func TestConnectContainerLifecycle(t *testing.T) {
	t.Parallel()
	c := realClient(t)
	ctx := t.Context()
	realm := "it-conn-" + testID
	meshMgr := mesh.NewManager(c, realm)
	ext := docker.ContainerName(realm + "-external")
	clusterNet := cluster.NetworkName(realm, "dev")

	t.Cleanup(func() {
		bg := context.Background()
		_ = c.KillContainer(bg, ext)
		_ = c.RemoveContainer(bg, ext)
		_ = c.RemoveNetwork(bg, clusterNet)
		_ = meshMgr.CleanupMesh(bg)
	})

	require.NoError(t, meshMgr.EnsureMesh(ctx))
	_, err := c.RunContainer(ctx, "--name", string(ext), "busybox:latest", "sleep", "120")
	require.NoError(t, err)

	_, _, err = executeWithRealm(realm, "connect", "container", string(ext))
	require.NoError(t, err)
	info, err := c.InspectContainer(ctx, ext)
	require.NoError(t, err)
	assert.Contains(t, info.IPs, meshMgr.NetworkName())

	external, err := cluster.ExternalContainers(ctx, c, meshMgr)
	require.NoError(t, err)
	assert.Equal(t, []docker.ContainerName{ext}, external)

	require.NoError(t, cluster.CreateClusterNetwork(ctx, c, realm, "dev"))
	cluster.ConnectExternal(ctx, c, meshMgr, clusterNet)
	info, err = c.InspectContainer(ctx, ext)
	require.NoError(t, err)
	assert.Contains(t, info.IPs, clusterNet)

	_, _, err = executeWithRealm(realm, "disconnect", "container", string(ext))
	require.NoError(t, err)
	info, err = c.InspectContainer(ctx, ext)
	require.NoError(t, err)
	assert.NotContains(t, info.IPs, meshMgr.NetworkName())
	assert.NotContains(t, info.IPs, clusterNet)

	// The realm goes with the container attached again.
	_, _, err = executeWithRealm(realm, "connect", "container", string(ext))
	require.NoError(t, err)
	_, _, err = executeWithRealm(realm, "delete", "realm")
	require.NoError(t, err)
	exists, err := c.NetworkExists(ctx, meshMgr.NetworkName())
	require.NoError(t, err)
	assert.False(t, exists, "mesh network removed")
	exists, err = c.ContainerExists(ctx, ext)
	require.NoError(t, err)
	assert.True(t, exists, "the external container stays")
}
