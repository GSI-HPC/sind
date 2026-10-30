// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/go-clikit/cobratree"
	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/state"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	assert.ErrorContains(t, err, "node argument required")
}

func TestParseSSHArgs_OnlyOptions(t *testing.T) {
	_, _, _, err := parseSSHArgs([]string{"-v", "-l", "alice", "--", "id"})
	require.ErrorContains(t, err, "node argument required")
	assert.True(t, cobratree.IsUsage(err))
}

// TestParseSSHArgs_OptionValues checks that the node is found among SSH
// options the way ssh's getopt reads them: with the values of options that
// take one, in the next argument or attached, and with options after the
// node.
func TestParseSSHArgs_OptionValues(t *testing.T) {
	for name, tc := range map[string]struct {
		args, opts []string
	}{
		"value in next argument":  {[]string{"-l", "alice", "worker-0"}, []string{"-l", "alice"}},
		"value attached":          {[]string{"-p2222", "worker-0"}, []string{"-p2222"}},
		"value of grouped option": {[]string{"-vL", "8080:localhost:80", "worker-0"}, []string{"-vL", "8080:localhost:80"}},
		"value attached in group": {[]string{"-vp2222", "worker-0"}, []string{"-vp2222"}},
		"value that looks like an option": {
			[]string{"-o", "-x", "worker-0"}, []string{"-o", "-x"},
		},
		"options after the node": {[]string{"worker-0", "-v", "-o", "BatchMode=yes"}, []string{"-v", "-o", "BatchMode=yes"}},
		"flag group":             {[]string{"-tt", "worker-0"}, []string{"-tt"}},
	} {
		t.Run(name, func(t *testing.T) {
			opts, node, cmd, err := parseSSHArgs(append(tc.args, "--", "id"))
			require.NoError(t, err)
			assert.Equal(t, tc.opts, opts)
			assert.Equal(t, "worker-0", node)
			assert.Equal(t, []string{"id"}, cmd)
		})
	}
}

// TestParseSSHArgs_CommandWithoutDash checks that a remote command written
// as for ssh, without the --, is a usage error that names it. sind took its
// last word for the node and the node for an SSH option, which ssh then
// failed to resolve in the relay.
func TestParseSSHArgs_CommandWithoutDash(t *testing.T) {
	for _, args := range [][]string{
		{"worker-0", "hostname"},
		{"-v", "worker-0", "hostname"},
		{"worker-0", "hostname", "--", "id"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, _, _, err := parseSSHArgs(args)
			require.Error(t, err)
			assert.True(t, cobratree.IsUsage(err))
			assert.EqualError(t, err, `unexpected argument "hostname" after node "worker-0": a remote command goes after --, as in sind ssh NODE -- COMMAND`)
		})
	}
}

func TestParseSSHArgs_MissingOptionValue(t *testing.T) {
	_, _, _, err := parseSSHArgs([]string{"worker-0", "-L", "--", "id"})
	require.Error(t, err)
	assert.True(t, cobratree.IsUsage(err))
	assert.EqualError(t, err, "ssh option -L needs a value")
}

func TestParseSSHArgs_LongOption(t *testing.T) {
	_, _, _, err := parseSSHArgs([]string{"--realm", "ci", "worker-0"})
	require.ErrorContains(t, err, "ssh has no option --realm")
	assert.True(t, cobratree.IsUsage(err))
}

func TestSSH_CommandWithoutDashExits2(t *testing.T) {
	var stderr bytes.Buffer
	code := run(t.Context(), []string{"ssh", "worker-0", "hostname"}, &stderr)
	assert.Equal(t, exitUsage, code)
	assert.Contains(t, stderr.String(), "a remote command goes after --")
}

func TestSSHOptionTakesNext(t *testing.T) {
	for arg, want := range map[string]bool{
		"-v":        false,
		"-tt":       false,
		"-L":        true,
		"-vL":       true,
		"-p2222":    false,
		"-vp2222":   false,
		"-o":        true,
		"-B":        true,
		"-P":        true,
		"-46AaCfGg": false,
	} {
		assert.Equal(t, want, sshOptionTakesNext(arg), arg)
	}
}

func TestCompleteSSHNodeArg_AfterGroupedFlagValue(t *testing.T) {
	sub := findCmd(completionCtx(&mock.Executor{}), t, "ssh")

	// -vL takes the next argument as its value, as -L does.
	names, directive := completeSSHNodeArg(sub, []string{"-vL"}, "8080")
	assert.Nil(t, names)
	assert.Equal(t, cobra.ShellCompDirectiveDefault, directive)

	// -p2222 has its value attached, so no node is there yet; and a node
	// after an option value is the node.
	names, directive = completeSSHNodeArg(sub, []string{"-p2222", "-l", "alice", "worker-0.dev"}, "")
	assert.Nil(t, names)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
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
	assert.ErrorContains(t, err, "-- separator")
}

func TestParseExecArgs_MissingCommand(t *testing.T) {
	_, _, err := parseExecArgs(nil, 0)
	assert.ErrorContains(t, err, "command required")
}

func TestParseExecArgs_ExtraArgsBefore(t *testing.T) {
	_, _, err := parseExecArgs([]string{"a", "b", "cmd"}, 2)
	assert.ErrorContains(t, err, "at most one argument before --")
}

// TestParseExecArgs_ClusterFromEnv covers SIND_CLUSTER, the cluster of exec
// without CLUSTER, which sind checks only then.
func TestParseExecArgs_ClusterFromEnv(t *testing.T) {
	t.Setenv("SIND_CLUSTER", "dev")
	cluster, _, err := parseExecArgs([]string{"hostname"}, 0)
	require.NoError(t, err)
	assert.Equal(t, "dev", cluster)

	t.Setenv("SIND_CLUSTER", "Dev")
	cluster, _, err = parseExecArgs([]string{"prod", "hostname"}, 1)
	require.NoError(t, err)
	assert.Equal(t, "prod", cluster)
	_, _, err = parseExecArgs([]string{"hostname"}, 0)
	require.EqualError(t, err, `SIND_CLUSTER: invalid cluster name "Dev": it has uppercase letters`)
	assert.False(t, cobratree.IsUsage(err))
}

func TestSSH_Help(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		t.Run(flag, func(t *testing.T) {
			stdout, _, err := executeCommand("ssh", flag)
			require.NoError(t, err)
			assert.Contains(t, stdout, "SSH into a cluster node")
			assert.Contains(t, stdout, "sind's\nown flags, such as --realm, go before ssh")
			assert.Contains(t, stdout, "  sind ssh [SSH_OPTIONS] [USER@]NODE [-- COMMAND [ARGS...]]\n")
		})
	}
}

func TestExec_Help(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		t.Run(flag, func(t *testing.T) {
			stdout, _, err := executeCommand("exec", flag)
			require.NoError(t, err)
			assert.Contains(t, stdout, "Run a command on the submitter or controller")
			assert.Contains(t, stdout, "  sind exec [flags] [CLUSTER] -- COMMAND [ARGS...]\n")
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

// TestExec_VerboseNote checks that exec writes the note of a SIND_VERBOSE
// that is no number once, and none when its own -v is in effect.
func TestExec_VerboseNote(t *testing.T) {
	t.Setenv("SIND_VERBOSE", "lots")
	for args, notes := range map[string]int{"exec -- hostname": 1, "exec -v -- hostname": 0} {
		var m mock.Executor
		m.AddResult("", "", assert.AnError) // docker ps for the exec target

		_, stderr, err := executeWithMock(&m, strings.Fields(args)...)

		require.Error(t, err)
		assert.Equal(t, notes, strings.Count(stderr, "SIND_VERBOSE is"), "%s: %s", args, stderr)
	}
}

func TestExec_UnknownFlag(t *testing.T) {
	_, _, err := executeCommand("exec", "--bogus", "--", "hostname")
	require.ErrorContains(t, err, "unknown flag: --bogus")
}

// dockerArgs runs sind with args, stdin and stdout, with the default
// cluster's submitter as the target of enter and exec, and returns the
// arguments of the docker command it ran.
func dockerArgs(t *testing.T, stdin io.Reader, stdout io.Writer, args ...string) []string {
	t.Helper()
	fakeDockerOnPath(t, "0")
	file := filepath.Join(t.TempDir(), "args")
	t.Setenv(fakeDockerArgsEnv, file)
	cmd := NewRootCommand()
	cmd.SetIn(stdin)
	cmd.SetOut(stdout)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(args)
	cmd.SetContext(submitterCtx(t.Context()))

	require.NoError(t, cmd.Execute())
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
			assert.Equal(t, tc.want, dockerArgs(t, noStdin(t), io.Discard, tc.args...))
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
	image := testutil.Image()
	dataDir := t.TempDir()

	realm := "it-ssh-" + testID
	cluster := "e2e-ssh-" + testID
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	cleanupRealm(t, c, realm)

	// Create cluster with a submitter for enter/exec routing tests.
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "cluster.yaml")
	cfg := "kind: Cluster\nname: " + cluster + "\ndefaults:\n  image: " + image + "\nnodes:\n  - controller\n  - submitter\n  - worker: 1\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfg), 0o644))

	stdout, stderr, err := executeWithRealmCtx(ctx, realm, "create", "cluster", "--config", cfgPath, "--data", dataDir)
	require.NoError(t, err, "create cluster: stdout=%q stderr=%q", stdout, stderr)

	// --- sind ssh: run command on specific node ---
	for _, node := range []string{"controller", "worker-0"} {
		stdout, stderr, err = executeWithRealmCtx(ctx, realm, "ssh", node+"."+cluster, "--", "hostname")
		require.NoError(t, err, "ssh %s: stdout=%q stderr=%q", node, stdout, stderr)
		assert.Contains(t, stdout, node)
	}

	// --- sind enter: routes to submitter when present ---
	// enter opens an interactive shell, which exits at once since stdin is
	// not a TTY: there is nothing to assert about an interactive session.
	_, _, _ = executeWithRealmCtx(ctx, realm, "enter", cluster)

	// --- sind exec: routes to submitter when present ---
	stdout, stderr, err = executeWithRealmCtx(ctx, realm, "exec", cluster, "--", "hostname")
	require.NoError(t, err, "exec: stdout=%q stderr=%q", stdout, stderr)
	assert.Contains(t, stdout, "submitter", "exec should route to submitter when present")

	// --- user SSH client via exported ssh_config ---
	sshConfigDir, err := state.RealmDir(realm)
	require.NoError(t, err)
	sshConfigPath := filepath.Join(sshConfigDir, "ssh_config")

	if _, statErr := os.Stat(sshConfigPath); statErr == nil {
		// ssh_config was exported, with a ProxyCommand for each node that
		// has the node's name in it rather than ssh's %h.
		data, readErr := os.ReadFile(sshConfigPath)
		require.NoError(t, readErr)
		for _, node := range []string{"controller", "submitter", "worker-0"} {
			assert.Contains(t, string(data), "\nHost "+node+"."+cluster+"."+realm+".sind\n")
		}
		assert.NotContains(t, string(data), "%h")

		// Test direct SSH access.
		for _, node := range []string{"controller", "worker-0"} {
			out, sshErr := exec.CommandContext(ctx, "ssh", "-F", sshConfigPath, "-o", "BatchMode=yes",
				node+"."+cluster+"."+realm+".sind", "hostname").CombinedOutput()
			require.NoError(t, sshErr, "user ssh %s: %s", node, string(out))
			assert.Contains(t, string(out), node)
		}
	}

	// --- delete cluster ---
	_, _, err = executeWithRealmCtx(ctx, realm, "delete", "cluster", cluster)
	require.NoError(t, err)
}

func TestIsTerminal_NotATerminal(t *testing.T) {
	// A reader or writer set with cmd.SetIn or cmd.SetOut, and a file that
	// is not a terminal.
	assert.False(t, isTerminal(strings.NewReader("")))
	assert.False(t, isTerminal(new(bytes.Buffer)))
	assert.False(t, isTerminal(noStdin(t)))
}
