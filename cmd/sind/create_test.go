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

func TestLoadConfig_FromStdin(t *testing.T) {
	// Replace os.Stdin with a pipe containing YAML config.
	r, w, err := os.Pipe()
	require.NoError(t, err)
	_, err = w.WriteString("kind: Cluster\nname: from-stdin\n")
	require.NoError(t, err)
	require.NoError(t, w.Close())

	origStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()

	cfg, err := loadConfig("")
	require.NoError(t, err)
	assert.Equal(t, "from-stdin", cfg.Name)
}

func TestLoadConfig_StdinInvalidYAML(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	_, err = w.WriteString("not: valid: yaml: [")
	require.NoError(t, err)
	require.NoError(t, w.Close())

	origStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()

	_, err = loadConfig("")
	assert.Error(t, err)
}

func TestCreateCluster_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	createCmd, _, err := cmd.Find([]string{"create", "cluster"})
	require.NoError(t, err)
	assert.Equal(t, "cluster [NAME] [--config FILE]", createCmd.Use)

	// Check flags exist with correct defaults
	assert.Nil(t, createCmd.Flags().Lookup("name"), "--name flag should not exist")
	assert.NotNil(t, createCmd.Flags().Lookup("config"))
	assert.NotNil(t, createCmd.Flags().Lookup("data"))
	assert.Equal(t, ".", createCmd.Flags().Lookup("data").DefValue)
}

func TestApplyDataFlag_HostPath(t *testing.T) {
	cfg, err := loadConfig("")
	require.NoError(t, err)

	require.NoError(t, applyDataFlag(cfg, "/tmp/my-project"))

	assert.Equal(t, "hostPath", cfg.Storage.DataStorage.Type)
	assert.Equal(t, "/tmp/my-project", cfg.Storage.DataStorage.HostPath)
}

func TestApplyDataFlag_RelativePath(t *testing.T) {
	cfg, err := loadConfig("")
	require.NoError(t, err)

	require.NoError(t, applyDataFlag(cfg, "."))

	assert.Equal(t, "hostPath", cfg.Storage.DataStorage.Type)
	assert.True(t, filepath.IsAbs(cfg.Storage.DataStorage.HostPath), "path should be absolute")
}

func TestApplyDataFlag_Volume(t *testing.T) {
	cfg, err := loadConfig("")
	require.NoError(t, err)

	require.NoError(t, applyDataFlag(cfg, "volume"))

	assert.Empty(t, cfg.Storage.DataStorage.Type)
	assert.Empty(t, cfg.Storage.DataStorage.HostPath)
}

func TestCreateCluster_RejectsTooManyArgs(t *testing.T) {
	_, _, err := executeCommand("create", "cluster", "name", "extra")
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
	t.Parallel()
	c := realClient(t)
	skipIfNoNsdelegate(t)
	image := testImage(t)
	dataDir := t.TempDir()

	realm := "it-e2e-" + testID
	cluster := "e2e-" + testID
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	meshMgr := mesh.NewManager(c, realm)
	t.Cleanup(func() {
		bg := context.Background()
		// Best-effort cleanup in case test fails partway.
		for _, name := range []string{"controller", "worker-0", "worker-1"} {
			cn := docker.ContainerName(realm + "-" + cluster + "-" + name)
			_ = c.KillContainer(bg, cn)
			_ = c.RemoveContainer(bg, cn)
		}
		for _, vt := range []string{"config", "munge", "data"} {
			_ = c.RemoveVolume(bg, docker.VolumeName(realm+"-"+cluster+"-"+vt))
		}
		_ = c.RemoveNetwork(bg, docker.NetworkName(realm+"-"+cluster+"-net"))
		_ = meshMgr.CleanupMesh(bg)
	})

	// Write a config file.
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "cluster.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("kind: Cluster\ndefaults:\n  image: "+image+"\n"), 0o644))

	// --- create cluster ---
	_, stderr, err := executeWithRealmCtx(ctx, realm, "create", "cluster", cluster, "--config", cfgPath, "--data", dataDir)
	require.NoError(t, err, "create cluster failed: stderr=%q", stderr)

	// --- get clusters ---
	var stdout string
	stdout, _, err = executeWithRealmCtx(ctx, realm, "get", "clusters")
	require.NoError(t, err)
	assert.Contains(t, stdout, cluster)
	assert.Contains(t, stdout, "running")

	// --- get nodes ---
	stdout, _, err = executeWithRealmCtx(ctx, realm, "get", "nodes", cluster)
	require.NoError(t, err)
	assert.Contains(t, stdout, "controller."+cluster)
	assert.Contains(t, stdout, "worker-0."+cluster)

	// --- get networks ---
	stdout, _, err = executeWithRealmCtx(ctx, realm, "get", "networks")
	require.NoError(t, err)
	assert.Contains(t, stdout, realm+"-"+cluster+"-net")
	assert.Contains(t, stdout, realm+"-mesh")

	// --- get volumes ---
	// Data volume is not created when using host-path bind mount (default).
	stdout, _, err = executeWithRealmCtx(ctx, realm, "get", "volumes")
	require.NoError(t, err)
	assert.Contains(t, stdout, realm+"-"+cluster+"-config")
	assert.Contains(t, stdout, realm+"-"+cluster+"-munge")

	// --- get munge-key ---
	stdout, _, err = executeWithRealmCtx(ctx, realm, "get", "munge-key", cluster)
	require.NoError(t, err)
	assert.NotEmpty(t, stdout)
	assert.NotContains(t, stdout, "Error")

	// --- status ---
	stdout, _, err = executeWithRealmCtx(ctx, realm, "status", cluster)
	require.NoError(t, err)
	assert.Contains(t, stdout, "CLUSTER")
	assert.Contains(t, stdout, cluster)
	assert.Contains(t, stdout, "NODES")
	assert.Contains(t, stdout, "controller")
	assert.Contains(t, stdout, "worker-0")
	assert.Contains(t, stdout, "NETWORKS")
	assert.Contains(t, stdout, "MESH SERVICES")
	assert.Contains(t, stdout, "MOUNTS")

	// --- exec ---
	stdout, stderr, err = executeWithRealmCtx(ctx, realm, "exec", cluster, "--", "hostname")
	require.NoError(t, err, "exec failed: stdout=%q stderr=%q", stdout, stderr)
	assert.Contains(t, stdout, "controller")

	// exec with default cluster
	_, _, err = executeWithRealmCtx(ctx, realm, "exec", "--", "echo", "hello")
	if err != nil {
		// Only fails if no "default" cluster exists — expected in isolation.
		assert.Contains(t, err.Error(), "default")
	}

	// exec missing separator
	_, _, err = executeWithRealmCtx(ctx, realm, "exec", "hostname")
	assert.Error(t, err)

	// exec missing command
	_, _, err = executeWithRealmCtx(ctx, realm, "exec", "--")
	assert.Error(t, err)

	// --- power shutdown ---
	node := "worker-0." + cluster
	_, _, err = executeWithRealmCtx(ctx, realm, "power", "shutdown", node)
	require.NoError(t, err)
	info, err := c.InspectContainer(ctx, docker.ContainerName(realm+"-"+cluster+"-worker-0"))
	require.NoError(t, err)
	assert.Equal(t, docker.StateExited, info.Status)

	// --- power on ---
	_, _, err = executeWithRealmCtx(ctx, realm, "power", "on", node)
	require.NoError(t, err)
	info, err = c.InspectContainer(ctx, docker.ContainerName(realm+"-"+cluster+"-worker-0"))
	require.NoError(t, err)
	assert.Equal(t, docker.StateRunning, info.Status)

	// --- power freeze / unfreeze ---
	_, _, err = executeWithRealmCtx(ctx, realm, "power", "freeze", node)
	require.NoError(t, err)
	info, _ = c.InspectContainer(ctx, docker.ContainerName(realm+"-"+cluster+"-worker-0"))
	assert.Equal(t, docker.StatePaused, info.Status)

	_, _, err = executeWithRealmCtx(ctx, realm, "power", "unfreeze", node)
	require.NoError(t, err)
	info, _ = c.InspectContainer(ctx, docker.ContainerName(realm+"-"+cluster+"-worker-0"))
	assert.Equal(t, docker.StateRunning, info.Status)

	// --- create worker ---
	_, stderr, err = executeWithRealmCtx(ctx, realm, "create", "worker", cluster, "--count", "1")
	require.NoError(t, err, "create worker failed: stderr=%q", stderr)

	// Verify new node appears.
	stdout, _, err = executeWithRealmCtx(ctx, realm, "get", "nodes", cluster)
	require.NoError(t, err)
	assert.Contains(t, stdout, "worker-1."+cluster)

	// --- delete worker ---
	_, _, err = executeWithRealmCtx(ctx, realm, "delete", "worker", "worker-1."+cluster)
	require.NoError(t, err)

	// Verify node is gone.
	stdout, _, err = executeWithRealmCtx(ctx, realm, "get", "nodes", cluster)
	require.NoError(t, err)
	assert.NotContains(t, stdout, "worker-1")

	// --- delete cluster ---
	_, _, err = executeWithRealmCtx(ctx, realm, "delete", "cluster", cluster)
	require.NoError(t, err)

	// Verify everything is gone.
	exists, err := c.ContainerExists(ctx, docker.ContainerName(realm+"-"+cluster+"-controller"))
	require.NoError(t, err)
	assert.False(t, exists)

	exists, err = c.NetworkExists(ctx, docker.NetworkName(realm+"-"+cluster+"-net"))
	require.NoError(t, err)
	assert.False(t, exists)

	// --- delete nonexistent cluster (idempotent) ---
	_, _, err = executeWithRealmCtx(ctx, realm, "delete", "cluster", "nonexistent-"+testID)
	require.NoError(t, err, "deleting nonexistent cluster should not error")
}
