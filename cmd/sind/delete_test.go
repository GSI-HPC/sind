// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeleteCluster_DefaultName(t *testing.T) {
	cmd := NewRootCommand()
	deleteCmd, _, err := cmd.Find([]string{"delete", "cluster"})
	require.NoError(t, err)
	assert.Equal(t, "cluster [NAME]", deleteCmd.Use)
}

func TestDeleteCluster_TooManyArgs(t *testing.T) {
	_, _, err := executeCommand("delete", "cluster", "a", "b")
	assert.Error(t, err)
}

func TestDeleteCluster_HasAllFlag(t *testing.T) {
	cmd := NewRootCommand()
	deleteCmd, _, err := cmd.Find([]string{"delete", "cluster"})
	require.NoError(t, err)
	f := deleteCmd.Flags().Lookup("all")
	require.NotNil(t, f)
	assert.Equal(t, "false", f.DefValue)
}

func TestDeleteCluster_AllRejectsArgs(t *testing.T) {
	_, _, err := executeCommand("delete", "cluster", "--all", "extra")
	assert.Error(t, err)
}

// --- Integration ---

func TestDeleteClusterLifecycle(t *testing.T) {
	t.Parallel()
	c := realClient(t)
	ctx := t.Context()
	realm := "it-del-" + testID
	cluster := "cli-del-" + testID

	meshMgr := mesh.NewManager(c, realm)

	netName := docker.NetworkName(realm + "-" + cluster + "-net")
	ctrName := docker.ContainerName(realm + "-" + cluster + "-controller")
	volConfig := docker.VolumeName(realm + "-" + cluster + "-config")
	volMunge := docker.VolumeName(realm + "-" + cluster + "-munge")
	volData := docker.VolumeName(realm + "-" + cluster + "-data")

	t.Cleanup(func() {
		bg := context.Background()
		_ = c.KillContainer(bg, ctrName)
		_ = c.RemoveContainer(bg, ctrName)
		_ = c.RemoveVolume(bg, volConfig)
		_ = c.RemoveVolume(bg, volMunge)
		_ = c.RemoveVolume(bg, volData)
		_ = c.RemoveNetwork(bg, netName)
		_ = meshMgr.CleanupMesh(bg)
	})

	// Mesh must exist for Delete to deregister DNS/known_hosts.
	require.NoError(t, meshMgr.EnsureMesh(ctx))

	// Create cluster resources.
	_, err := c.CreateNetwork(ctx, netName, nil)
	require.NoError(t, err)
	require.NoError(t, c.CreateVolume(ctx, volConfig, nil))
	require.NoError(t, c.CreateVolume(ctx, volMunge, nil))
	require.NoError(t, c.CreateVolume(ctx, volData, nil))
	_, err = c.RunContainer(ctx,
		"--name", string(ctrName),
		"--network", string(netName),
		"--label", "sind.realm="+realm,
		"--label", "sind.cluster="+cluster,
		"--label", "sind.role=controller",
		"busybox:latest", "sleep", "120",
	)
	require.NoError(t, err)

	// Delete via CLI.
	_, _, err = executeWithRealm(realm, "delete", "cluster", cluster)
	require.NoError(t, err)

	// Verify cluster resources are gone.
	exists, err := c.ContainerExists(ctx, ctrName)
	require.NoError(t, err)
	assert.False(t, exists, "container should be removed")

	exists, err = c.NetworkExists(ctx, netName)
	require.NoError(t, err)
	assert.False(t, exists, "network should be removed")

	exists, err = c.VolumeExists(ctx, volConfig)
	require.NoError(t, err)
	assert.False(t, exists, "config volume should be removed")
}
