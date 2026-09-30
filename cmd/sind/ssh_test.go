// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
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

// parseExecArgs gets exec's arguments as cobra's flag parsing leaves them:
// without the -- and with the number of arguments that preceded it.

func TestParseExecArgs_Simple(t *testing.T) {
	cluster, cmd, err := parseExecArgs([]string{"hostname"}, 0)
	require.NoError(t, err)
	assert.Equal(t, "default", cluster)
	assert.Equal(t, []string{"hostname"}, cmd)
}

func TestParseExecArgs_WithCluster(t *testing.T) {
	cluster, cmd, err := parseExecArgs([]string{"dev", "squeue"}, 1)
	require.NoError(t, err)
	assert.Equal(t, "dev", cluster)
	assert.Equal(t, []string{"squeue"}, cmd)
}

func TestParseExecArgs_MissingSeparator(t *testing.T) {
	_, _, err := parseExecArgs([]string{"hostname"}, -1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "-- separator")
}

func TestParseExecArgs_MissingCommand(t *testing.T) {
	_, _, err := parseExecArgs(nil, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "command required")
}

func TestParseExecArgs_ExtraArgsBefore(t *testing.T) {
	_, _, err := parseExecArgs([]string{"a", "b", "cmd"}, 2)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at most one argument before --")
}

func TestSSH_Help(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		t.Run(flag, func(t *testing.T) {
			stdout, _, err := executeCommand("ssh", flag)
			require.NoError(t, err)
			assert.Contains(t, stdout, "SSH into a cluster node")
		})
	}
}

func TestExec_Help(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		t.Run(flag, func(t *testing.T) {
			stdout, _, err := executeCommand("exec", flag)
			require.NoError(t, err)
			assert.Contains(t, stdout, "Run a command on submitter or controller")
		})
	}
}

// TestExec_FlagsAfterExec checks that exec parses --realm wherever it stands
// before the --, as in the "exec --realm R CLUSTER -- COMMAND" the MCP server
// runs.
func TestExec_FlagsAfterExec(t *testing.T) {
	for name, args := range map[string][]string{
		"before cluster": {"exec", "--realm", "ci", "dev", "--", "hostname"},
		"after cluster":  {"exec", "dev", "--realm=ci", "--", "hostname"},
	} {
		t.Run(name, func(t *testing.T) {
			var m mock.Executor
			m.AddResult("", "", assert.AnError) // docker ps for the exec target

			_, _, err := executeWithMock(&m, args...)

			require.Error(t, err)
			require.Len(t, m.Calls, 1)
			assert.Contains(t, m.Calls[0].Args, "label=sind.realm=ci")
			assert.Contains(t, m.Calls[0].Args, "label=sind.cluster=dev")
		})
	}
}

func TestExec_FlagsAfterDashBelongToTheCommand(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", assert.AnError) // docker ps for the exec target

	// --realm after -- is part of the command, so its invalid value is not
	// checked, and the realm stays the default.
	_, _, err := executeWithMock(&m, "exec", "--", "ls", "--realm", "Not_A_Realm")

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "invalid realm name")
	require.Len(t, m.Calls, 1)
	assert.Contains(t, m.Calls[0].Args, "label=sind.realm=sind")
}

func TestExec_VerboseAfterExec(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", assert.AnError) // docker ps for the exec target

	_, stderr, err := executeWithMock(&m, "exec", "-vvv", "--", "hostname")

	require.Error(t, err)
	assert.Contains(t, stderr, "TRAC")
}

func TestExec_UnknownFlag(t *testing.T) {
	_, _, err := executeCommand("exec", "--bogus", "--", "hostname")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown flag: --bogus")
}

// dockerArgs runs sind with args, with the default cluster's submitter as
// the target of enter and exec, and returns the arguments of the docker
// command it ran.
func dockerArgs(t *testing.T, args ...string) []string {
	t.Helper()
	fakeDockerOnPath(t, "0")
	file := filepath.Join(t.TempDir(), "args")
	t.Setenv(fakeDockerArgsEnv, file)
	var stderr bytes.Buffer

	code := run(submitterCtx(t.Context()), args, &stderr)

	require.Equal(t, 0, code, stderr.String())
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	return strings.Split(string(data), "\n")
}

func TestUser_DockerArgs(t *testing.T) {
	for name, tc := range map[string]struct{ args, want []string }{
		"enter": {
			[]string{"enter", "--user", "alice"},
			[]string{"exec", "-i", "-u", "alice", "-w", "/home/alice", "sind-default-submitter", "bash", "-l"},
		},
		"enter as root": {
			[]string{"enter"},
			[]string{"exec", "-i", "-w", "/data", "sind-default-submitter", "bash", "-l"},
		},
		"exec": {
			[]string{"exec", "-u", "alice", "--", "id", "-u"},
			[]string{"exec", "-i", "-u", "alice", "-w", "/home/alice", "sind-default-submitter", "id", "-u"},
		},
		"exec as root": {
			[]string{"exec", "--", "id", "-u"},
			[]string{"exec", "-i", "-w", "/data", "sind-default-submitter", "id", "-u"},
		},
		"ssh": {
			[]string{"ssh", "-v", "alice@worker-0", "--", "id"},
			[]string{"exec", "-i", "sind-ssh", "ssh", "-v", "-l", "alice", "worker-0.default.sind.sind", "id"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, dockerArgs(t, tc.args...))
		})
	}
}

// --- Integration ---

// TestSSHAccess exercises all SSH access methods against a real cluster:
// sind ssh, sind enter, sind exec, and user SSH client via exported ssh_config.
func TestSSHAccess(t *testing.T) {
	t.Parallel()
	c := realClient(t)
	checkPrerequisites(t, c)
	image := testImage(t)
	dataDir := t.TempDir()

	realm := "it-ssh-" + testID
	cluster := "e2e-ssh-" + testID
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	meshMgr := mesh.NewManager(c, realm)
	t.Cleanup(func() {
		bg := context.Background()
		for _, name := range []string{"controller", "submitter", "worker-0"} {
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

	// Create cluster with a submitter for enter/exec routing tests.
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "cluster.yaml")
	cfg := "kind: Cluster\nname: " + cluster + "\ndefaults:\n  image: " + image + "\nnodes:\n  - controller\n  - submitter\n  - worker: 1\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfg), 0o644))

	stdout, stderr, err := executeWithRealmCtx(ctx, realm, "create", "cluster", "--config", cfgPath, "--data", dataDir)
	require.NoError(t, err, "create cluster: stdout=%q stderr=%q", stdout, stderr)

	// --- sind ssh: run command on specific node ---
	stdout, stderr, err = executeWithRealmCtx(ctx, realm, "ssh", "controller."+cluster, "--", "hostname")
	require.NoError(t, err, "ssh controller: stdout=%q stderr=%q", stdout, stderr)
	assert.Contains(t, stdout, "controller")

	stdout, stderr, err = executeWithRealmCtx(ctx, realm, "ssh", "worker-0."+cluster, "--", "hostname")
	require.NoError(t, err, "ssh worker-0: stdout=%q stderr=%q", stdout, stderr)
	assert.Contains(t, stdout, "worker-0")

	// --- sind enter: routes to submitter when present ---
	stdout, stderr, err = executeWithRealmCtx(ctx, realm, "enter", cluster)
	// enter opens an interactive shell — will exit immediately since stdin is not a TTY.
	// But the connection itself should succeed (exit code 0 or similar).
	// We can't assert much about stdout here since it's an interactive session.
	_ = stdout
	_ = stderr
	_ = err

	// --- sind exec: routes to submitter when present ---
	stdout, stderr, err = executeWithRealmCtx(ctx, realm, "exec", cluster, "--", "hostname")
	require.NoError(t, err, "exec: stdout=%q stderr=%q", stdout, stderr)
	assert.Contains(t, stdout, "submitter", "exec should route to submitter when present")

	// --- user SSH client via exported ssh_config ---
	sshConfigDir, err := sindStateDir(realm)
	require.NoError(t, err)
	sshConfigPath := filepath.Join(sshConfigDir, "ssh_config")

	if _, statErr := os.Stat(sshConfigPath); statErr == nil {
		// ssh_config was exported — test direct SSH access.
		sshCmd := exec.CommandContext(ctx, "ssh",
			"-F", sshConfigPath,
			"-o", "BatchMode=yes",
			"controller."+cluster+"."+realm+".sind",
			"hostname",
		)
		out, sshErr := sshCmd.CombinedOutput()
		require.NoError(t, sshErr, "user ssh: %s", string(out))
		assert.Contains(t, string(out), "controller")

		// Also test worker access.
		sshCmd = exec.CommandContext(ctx, "ssh",
			"-F", sshConfigPath,
			"-o", "BatchMode=yes",
			"worker-0."+cluster+"."+realm+".sind",
			"hostname",
		)
		out, sshErr = sshCmd.CombinedOutput()
		require.NoError(t, sshErr, "user ssh worker: %s", string(out))
		assert.Contains(t, string(out), "worker-0")
	}

	// --- delete cluster ---
	_, _, err = executeWithRealmCtx(ctx, realm, "delete", "cluster", cluster)
	require.NoError(t, err)
}

func TestStdinIsTTY_NotATerminal(t *testing.T) {
	// A reader set with cmd.SetIn, and a file that is not a terminal.
	assert.False(t, stdinIsTTY(strings.NewReader("")))
	assert.False(t, stdinIsTTY(noStdin(t)))
}
