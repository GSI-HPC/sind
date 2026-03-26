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
	c := realClient(t)
	ctx := t.Context()
	t.Setenv("SIND_REALM", testRealm)
	cluster := "cli-del-" + testID

	meshMgr := mesh.NewManager(c, testRealm)

	netName := docker.NetworkName(testRealm + "-" + cluster + "-net")
	ctrName := docker.ContainerName(testRealm + "-" + cluster + "-controller")
	volConfig := docker.VolumeName(testRealm + "-" + cluster + "-config")
	volMunge := docker.VolumeName(testRealm + "-" + cluster + "-munge")
	volData := docker.VolumeName(testRealm + "-" + cluster + "-data")

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
		"--label", "sind.realm="+testRealm,
		"--label", "sind.cluster="+cluster,
		"--label", "sind.role=controller",
		"busybox:latest", "sleep", "120",
	)
	require.NoError(t, err)

	// Delete via CLI.
	stdout, _, err := executeWithDocker("delete", "cluster", cluster)
	require.NoError(t, err)
	assert.Contains(t, stdout, "deleted")

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
