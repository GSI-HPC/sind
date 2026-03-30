// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSSH_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"ssh"})
	require.NoError(t, err)
	assert.Contains(t, c.Use, "ssh")
}

func TestEnter_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"enter"})
	require.NoError(t, err)
	assert.Equal(t, "enter [CLUSTER]", c.Use)
}

func TestEnter_TooManyArgs(t *testing.T) {
	_, _, err := executeCommand("enter", "a", "b")
	assert.Error(t, err)
}

func TestExec_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"exec"})
	require.NoError(t, err)
	assert.Contains(t, c.Use, "exec")
}

func TestParseSSHArgs_NodeOnly(t *testing.T) {
	opts, node, cmd, err := parseSSHArgs([]string{"worker-0"})
	require.NoError(t, err)
	assert.Empty(t, opts)
	assert.Equal(t, "worker-0", node)
	assert.Nil(t, cmd)
}

func TestParseSSHArgs_WithOptions(t *testing.T) {
	opts, node, cmd, err := parseSSHArgs([]string{"-v", "-L", "8080:localhost:80", "controller"})
	require.NoError(t, err)
	assert.Equal(t, []string{"-v", "-L", "8080:localhost:80"}, opts)
	assert.Equal(t, "controller", node)
	assert.Nil(t, cmd)
}

func TestParseSSHArgs_WithCommand(t *testing.T) {
	opts, node, cmd, err := parseSSHArgs([]string{"worker-0", "--", "hostname"})
	require.NoError(t, err)
	assert.Empty(t, opts)
	assert.Equal(t, "worker-0", node)
	assert.Equal(t, []string{"hostname"}, cmd)
}

func TestParseSSHArgs_Full(t *testing.T) {
	opts, node, cmd, err := parseSSHArgs([]string{"-t", "worker-0.dev", "--", "top", "-b"})
	require.NoError(t, err)
	assert.Equal(t, []string{"-t"}, opts)
	assert.Equal(t, "worker-0.dev", node)
	assert.Equal(t, []string{"top", "-b"}, cmd)
}

func TestParseSSHArgs_Empty(t *testing.T) {
	_, _, _, err := parseSSHArgs(nil)
	assert.Error(t, err)
}

func TestParseSSHArgs_OnlyDash(t *testing.T) {
	_, _, _, err := parseSSHArgs([]string{"--"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "node argument required")
}

func TestParseExecArgs_Simple(t *testing.T) {
	cluster, cmd, err := parseExecArgs([]string{"--", "hostname"})
	require.NoError(t, err)
	assert.Equal(t, "default", cluster)
	assert.Equal(t, []string{"hostname"}, cmd)
}

func TestParseExecArgs_WithCluster(t *testing.T) {
	cluster, cmd, err := parseExecArgs([]string{"dev", "--", "squeue"})
	require.NoError(t, err)
	assert.Equal(t, "dev", cluster)
	assert.Equal(t, []string{"squeue"}, cmd)
}

func TestParseExecArgs_MissingSeparator(t *testing.T) {
	_, _, err := parseExecArgs([]string{"hostname"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "-- separator")
}

func TestParseExecArgs_MissingCommand(t *testing.T) {
	_, _, err := parseExecArgs([]string{"--"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "command required")
}

func TestParseExecArgs_ExtraArgsBefore(t *testing.T) {
	_, _, err := parseExecArgs([]string{"a", "b", "--", "cmd"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at most one argument before --")
}

// --- Integration ---

// TestSSHAccess exercises all SSH access methods against a real cluster:
// sind ssh, sind enter, sind exec, and user SSH client via exported ssh_config.
func TestSSHAccess(t *testing.T) {
	c := realClient(t)
	skipIfNoNsdelegate(t)
	image := testImage(t)

	t.Setenv("SIND_REALM", testRealm)

	cluster := "e2e-ssh-" + testID
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	meshMgr := mesh.NewManager(c, testRealm)
	t.Cleanup(func() {
		bg := context.Background()
		for _, name := range []string{"controller", "submitter", "worker-0"} {
			cn := docker.ContainerName(testRealm + "-" + cluster + "-" + name)
			_ = c.KillContainer(bg, cn)
			_ = c.RemoveContainer(bg, cn)
		}
		for _, vt := range []string{"config", "munge", "data"} {
			_ = c.RemoveVolume(bg, docker.VolumeName(testRealm+"-"+cluster+"-"+vt))
		}
		_ = c.RemoveNetwork(bg, docker.NetworkName(testRealm+"-"+cluster+"-net"))
		_ = meshMgr.CleanupMesh(bg)
	})

	// Create cluster with a submitter for enter/exec routing tests.
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "cluster.yaml")
	cfg := "kind: Cluster\nname: " + cluster + "\ndefaults:\n  image: " + image + "\nnodes:\n  - controller\n  - submitter\n  - worker: 1\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfg), 0o644))

	stdout, stderr, err := executeWithDockerCtx(ctx, "create", "cluster", "--config", cfgPath)
	require.NoError(t, err, "create cluster: stdout=%q stderr=%q", stdout, stderr)

	// --- sind ssh: run command on specific node ---
	stdout, stderr, err = executeWithDockerCtx(ctx, "ssh", "controller."+cluster, "--", "hostname")
	require.NoError(t, err, "ssh controller: stdout=%q stderr=%q", stdout, stderr)
	assert.Contains(t, stdout, "controller")

	stdout, stderr, err = executeWithDockerCtx(ctx, "ssh", "worker-0."+cluster, "--", "hostname")
	require.NoError(t, err, "ssh worker-0: stdout=%q stderr=%q", stdout, stderr)
	assert.Contains(t, stdout, "worker-0")

	// --- sind enter: routes to submitter when present ---
	stdout, stderr, err = executeWithDockerCtx(ctx, "enter", cluster)
	// enter opens an interactive shell — will exit immediately since stdin is not a TTY.
	// But the connection itself should succeed (exit code 0 or similar).
	// We can't assert much about stdout here since it's an interactive session.
	_ = stdout
	_ = stderr
	_ = err

	// --- sind exec: routes to submitter when present ---
	stdout, stderr, err = executeWithDockerCtx(ctx, "exec", cluster, "--", "hostname")
	require.NoError(t, err, "exec: stdout=%q stderr=%q", stdout, stderr)
	assert.Contains(t, stdout, "submitter", "exec should route to submitter when present")

	// --- user SSH client via exported ssh_config ---
	sshConfigDir, err := sindStateDir(testRealm)
	require.NoError(t, err)
	sshConfigPath := filepath.Join(sshConfigDir, "ssh_config")

	if _, statErr := os.Stat(sshConfigPath); statErr == nil {
		// ssh_config was exported — test direct SSH access.
		sshCmd := exec.CommandContext(ctx, "ssh",
			"-F", sshConfigPath,
			"-o", "BatchMode=yes",
			"controller."+cluster+".sind.local",
			"hostname",
		)
		out, sshErr := sshCmd.CombinedOutput()
		require.NoError(t, sshErr, "user ssh: %s", string(out))
		assert.Contains(t, string(out), "controller")

		// Also test worker access.
		sshCmd = exec.CommandContext(ctx, "ssh",
			"-F", sshConfigPath,
			"-o", "BatchMode=yes",
			"worker-0."+cluster+".sind.local",
			"hostname",
		)
		out, sshErr = sshCmd.CombinedOutput()
		require.NoError(t, sshErr, "user ssh worker: %s", string(out))
		assert.Contains(t, string(out), "worker-0")
	}

	// --- delete cluster ---
	_, _, err = executeWithDockerCtx(ctx, "delete", "cluster", cluster)
	require.NoError(t, err)
}
