// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
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

// emptyRealmCtx returns a context whose docker client answers the realm
// lock like dockerd, a realm without SSH relay, and, with psErr, every
// docker ps with that error, else with no container. It also points
// XDG_STATE_HOME at a directory holding a stale ssh_config export of realm
// sind, whose path it returns.
func emptyRealmCtx(t *testing.T, psErr error) (context.Context, string) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	export := filepath.Join(stateHome, "sind", "sind", "ssh_config")
	require.NoError(t, os.MkdirAll(filepath.Dir(export), 0o700))
	require.NoError(t, os.WriteFile(export, []byte("stale"), 0o600))

	d := newLockDaemon(t)
	exit1 := testutil.ExitCode1(t)
	m := &mock.Executor{OnCall: func(args []string, stdin string) mock.Result {
		switch {
		case args[0] == "network" && (args[1] == "create" || args[1] == "inspect" || args[1] == "rm"):
			return d.onCall(args, stdin)
		case args[0] == "exec":
			return mock.Result{Stderr: testutil.NoSuchContainer(args[1]), Err: exit1}
		case args[0] == "ps" && psErr != nil:
			return mock.Result{Err: psErr}
		}
		return mock.Result{}
	}}
	return withClient(t.Context(), docker.NewClient(m)), export
}

// TestDeleteWorker_AcrossClusters covers workers of two clusters that both
// fail: each cluster is tried, the failures are joined, and the SSH export
// is brought up to date anyway.
func TestDeleteWorker_AcrossClusters(t *testing.T) {
	ctx, export := emptyRealmCtx(t, nil)

	_, _, err := execute(ctx, nil, "delete", "worker", "worker-0.dev", "worker-0.prod")

	require.ErrorContains(t, err, `cluster "dev" not found in realm "sind"`)
	require.ErrorContains(t, err, `cluster "prod" not found in realm "sind"`)
	assert.NoFileExists(t, export, "SSH export synced")
}

// TestDeleteCluster_SyncsExportOnFailure covers a delete that fails: the
// SSH export is brought up to date anyway, as with --all.
func TestDeleteCluster_SyncsExportOnFailure(t *testing.T) {
	ctx, export := emptyRealmCtx(t, fmt.Errorf("daemon unreachable"))

	_, _, err := execute(ctx, nil, "delete", "cluster", "dev")

	require.ErrorContains(t, err, "daemon unreachable")
	assert.NoFileExists(t, export, "SSH export synced")
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

// TestDeleteRealmAndAll_Help verifies that delete realm and delete cluster
// --all, which run the same code, describe the same scope and name each
// other.
func TestDeleteRealmAndAll_Help(t *testing.T) {
	const scope = "every cluster of the realm, its mesh, its monitoring stack"
	stdout, _, err := executeCommand("delete", "cluster", "--help")
	require.NoError(t, err)
	assert.Contains(t, stdout, "--all deletes "+scope)
	assert.Contains(t, stdout, "as delete realm does")

	stdout, _, err = executeCommand("delete", "realm", "--help")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Delete "+scope)
	assert.Contains(t, stdout, "as delete cluster --all does")
}
