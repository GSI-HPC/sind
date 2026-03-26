// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig_Default(t *testing.T) {
	cfg, err := loadConfig("")
	require.NoError(t, err)
	assert.Equal(t, "Cluster", cfg.Kind)
	assert.Equal(t, "default", cfg.Name)
}

func TestLoadConfig_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := []byte("kind: Cluster\nname: test\n")
	require.NoError(t, os.WriteFile(path, data, 0o644))

	cfg, err := loadConfig(path)
	require.NoError(t, err)
	assert.Equal(t, "test", cfg.Name)
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := loadConfig("/nonexistent/config.yaml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading config")
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("not: valid: yaml: ["), 0o644))

	_, err := loadConfig(path)
	assert.Error(t, err)
}

func TestCreateCluster_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	createCmd, _, err := cmd.Find([]string{"create", "cluster"})
	require.NoError(t, err)
	assert.Equal(t, "cluster [--name NAME] [--config FILE]", createCmd.Use)

	// Check flags exist with correct defaults
	assert.NotNil(t, createCmd.Flags().Lookup("name"))
	assert.NotNil(t, createCmd.Flags().Lookup("config"))
	assert.Equal(t, "default", createCmd.Flags().Lookup("name").DefValue)
}

func TestCreateCluster_RejectsArgs(t *testing.T) {
	_, _, err := executeCommand("create", "cluster", "extra-arg")
	assert.Error(t, err)
}

func TestLoadConfig_PreservesName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := []byte("kind: Cluster\nname: from-file\n")
	require.NoError(t, os.WriteFile(path, data, 0o644))

	cfg, err := loadConfig(path)
	require.NoError(t, err)
	assert.Equal(t, "from-file", cfg.Name)
}

// --- Integration ---

// TestClusterLifecycle exercises the full user workflow via CLI commands:
// create cluster → get/status → power ops → add worker → delete worker → delete cluster.
func TestClusterLifecycle(t *testing.T) {
	c := realClient(t)
	skipIfNoNsdelegate(t)
	image := skipIfNoImage(t, c)

	cluster := "e2e-" + testID
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	meshMgr := mesh.NewManager(c, mesh.DefaultRealm)
	t.Cleanup(func() {
		bg := context.Background()
		// Best-effort cleanup in case test fails partway.
		for _, name := range []string{"controller", "worker-0", "worker-1"} {
			cn := docker.ContainerName("sind-" + cluster + "-" + name)
			_ = c.KillContainer(bg, cn)
			_ = c.RemoveContainer(bg, cn)
		}
		for _, vt := range []string{"config", "munge", "data"} {
			_ = c.RemoveVolume(bg, docker.VolumeName("sind-"+cluster+"-"+vt))
		}
		_ = c.RemoveNetwork(bg, docker.NetworkName("sind-"+cluster+"-net"))
		_ = meshMgr.CleanupMesh(bg)
	})

	// Write a config file.
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "cluster.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("kind: Cluster\nname: "+cluster+"\ndefaults:\n  image: "+image+"\n"), 0o644))

	// --- create cluster ---
	stdout, stderr, err := executeWithDockerCtx(ctx, "create", "cluster", "--config", cfgPath)
	require.NoError(t, err, "create cluster failed: stdout=%q stderr=%q", stdout, stderr)
	assert.Contains(t, stdout, "created")
	assert.Contains(t, stdout, "2 node(s)")

	// --- get clusters ---
	stdout, _, err = executeWithDockerCtx(ctx, "get", "clusters")
	require.NoError(t, err)
	assert.Contains(t, stdout, cluster)
	assert.Contains(t, stdout, "running")

	// --- get nodes ---
	stdout, _, err = executeWithDockerCtx(ctx, "get", "nodes", cluster)
	require.NoError(t, err)
	assert.Contains(t, stdout, "controller."+cluster)
	assert.Contains(t, stdout, "worker-0."+cluster)

	// --- get networks ---
	stdout, _, err = executeWithDockerCtx(ctx, "get", "networks")
	require.NoError(t, err)
	assert.Contains(t, stdout, "sind-"+cluster+"-net")
	assert.Contains(t, stdout, "sind-mesh")

	// --- get volumes ---
	stdout, _, err = executeWithDockerCtx(ctx, "get", "volumes")
	require.NoError(t, err)
	assert.Contains(t, stdout, "sind-"+cluster+"-config")
	assert.Contains(t, stdout, "sind-"+cluster+"-munge")
	assert.Contains(t, stdout, "sind-"+cluster+"-data")

	// --- get munge-key ---
	stdout, _, err = executeWithDockerCtx(ctx, "get", "munge-key", cluster)
	require.NoError(t, err)
	assert.NotEmpty(t, stdout)
	assert.NotContains(t, stdout, "Error")

	// --- status ---
	stdout, _, err = executeWithDockerCtx(ctx, "status", cluster)
	require.NoError(t, err)
	assert.Contains(t, stdout, "Cluster: "+cluster)
	assert.Contains(t, stdout, "NODES")
	assert.Contains(t, stdout, "controller")
	assert.Contains(t, stdout, "worker-0")
	assert.Contains(t, stdout, "NETWORK")
	assert.Contains(t, stdout, "VOLUMES")

	// --- exec ---
	stdout, stderr, err = executeWithDockerCtx(ctx, "exec", cluster, "--", "hostname")
	require.NoError(t, err, "exec failed: stdout=%q stderr=%q", stdout, stderr)
	assert.Contains(t, stdout, "controller")

	// exec with default cluster
	stdout, _, err = executeWithDockerCtx(ctx, "exec", "--", "echo", "hello")
	if err != nil {
		// Only fails if no "default" cluster exists — expected in isolation.
		assert.Contains(t, err.Error(), "default")
	}

	// exec missing separator
	_, _, err = executeWithDockerCtx(ctx, "exec", "hostname")
	assert.Error(t, err)

	// exec missing command
	_, _, err = executeWithDockerCtx(ctx, "exec", "--")
	assert.Error(t, err)

	// --- power shutdown ---
	node := "worker-0." + cluster
	_, _, err = executeWithDockerCtx(ctx, "power", "shutdown", node)
	require.NoError(t, err)
	info, err := c.InspectContainer(ctx, docker.ContainerName("sind-"+cluster+"-worker-0"))
	require.NoError(t, err)
	assert.Equal(t, "exited", info.Status)

	// --- power on ---
	_, _, err = executeWithDockerCtx(ctx, "power", "on", node)
	require.NoError(t, err)
	info, err = c.InspectContainer(ctx, docker.ContainerName("sind-"+cluster+"-worker-0"))
	require.NoError(t, err)
	assert.Equal(t, "running", info.Status)

	// --- power freeze / unfreeze ---
	_, _, err = executeWithDockerCtx(ctx, "power", "freeze", node)
	require.NoError(t, err)
	info, _ = c.InspectContainer(ctx, docker.ContainerName("sind-"+cluster+"-worker-0"))
	assert.Equal(t, "paused", info.Status)

	_, _, err = executeWithDockerCtx(ctx, "power", "unfreeze", node)
	require.NoError(t, err)
	info, _ = c.InspectContainer(ctx, docker.ContainerName("sind-"+cluster+"-worker-0"))
	assert.Equal(t, "running", info.Status)

	// --- create worker ---
	stdout, stderr, err = executeWithDockerCtx(ctx, "create", "worker", cluster, "--count", "1")
	require.NoError(t, err, "create worker failed: stdout=%q stderr=%q", stdout, stderr)
	assert.Contains(t, stdout, "worker-1")

	// Verify new node appears.
	stdout, _, err = executeWithDockerCtx(ctx, "get", "nodes", cluster)
	require.NoError(t, err)
	assert.Contains(t, stdout, "worker-1."+cluster)

	// --- delete worker ---
	stdout, _, err = executeWithDockerCtx(ctx, "delete", "worker", "worker-1."+cluster)
	require.NoError(t, err)
	assert.Contains(t, stdout, "Removed")

	// Verify node is gone.
	stdout, _, err = executeWithDockerCtx(ctx, "get", "nodes", cluster)
	require.NoError(t, err)
	assert.NotContains(t, stdout, "worker-1")

	// --- delete cluster ---
	stdout, _, err = executeWithDockerCtx(ctx, "delete", "cluster", cluster)
	require.NoError(t, err)
	assert.Contains(t, stdout, "deleted")

	// Verify everything is gone.
	exists, err := c.ContainerExists(ctx, docker.ContainerName("sind-"+cluster+"-controller"))
	require.NoError(t, err)
	assert.False(t, exists)

	exists, err = c.NetworkExists(ctx, docker.NetworkName("sind-"+cluster+"-net"))
	require.NoError(t, err)
	assert.False(t, exists)

	// --- delete nonexistent cluster (idempotent) ---
	_, _, err = executeWithDockerCtx(ctx, "delete", "cluster", "nonexistent-"+testID)
	require.NoError(t, err, "deleting nonexistent cluster should not error")
}
