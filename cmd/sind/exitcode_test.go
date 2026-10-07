// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noDocker returns a context whose docker client fails every call, so that
// a command that gets as far as docker exits 1, not 2.
func noDocker(ctx context.Context) context.Context {
	return withClient(ctx, docker.NewClient(&mock.Executor{}))
}

// TestRun_UsageErrorExits2 checks that a command line sind rejects exits 2,
// wherever it is rejected: by pflag while cobra traverses the parents
// (TraverseChildren) or parses the command's own flags, by an argument
// check, or by the command itself before it acts. It exited 1, like a
// command that ran and failed.
func TestRun_UsageErrorExits2(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--bogus", "get", "clusters"}, "unknown flag: --bogus"},
		{[]string{"--verbose=x", "version"}, `invalid argument "x" for "-v, --verbose" flag`},
		{[]string{"get", "clusters", "--bogus"}, "unknown flag: --bogus"},
		{[]string{"get", "clusters", "-o"}, "flag needs an argument"},
		{[]string{"get", "clusters", "---o"}, "bad flag syntax: ---o"},
		{[]string{"bogus"}, `unknown command "bogus" for "sind"`},
		{[]string{"get", "bogus"}, `unknown command "bogus" for "sind get"`},
		{[]string{"help", "bogus"}, `unknown help topic "bogus"`},
		{[]string{"completion", "fishy"}, `unknown command "fishy" for "sind completion"`},
		{[]string{"version", "extra"}, `unknown command "extra" for "sind version"`},
		{[]string{"mcp", "tools", "extra"}, `unknown command "extra" for "sind mcp tools"`},
		{[]string{"mcp", "stream", "extra"}, `unknown command "extra" for "sind mcp stream"`},
		{[]string{"mcp", "claude", "list", "extra"}, `unknown command "extra" for "sind mcp claude list"`},
		{[]string{"get", "cluster", "a", "b"}, "accepts at most 1 arg(s), received 2"},
		{[]string{"get", "cluster", "Not_A_Name"}, `invalid cluster name "Not_A_Name"`},
		{[]string{"get", "clusters", "-o", "yaml"}, `invalid --output value "yaml"`},
		{[]string{"get", "auth-key", "--type", "kerberos"}, `invalid --type value "kerberos": must be munge, slurm, jwt`},
		{[]string{"--realm", "Not_A_Realm", "get", "clusters"}, `--realm: invalid realm name "Not_A_Realm"`},
		{[]string{"get", "node", "worker-0.dev.sind.sind"}, "not the FQDN"},
		{[]string{"get", "node", "worker-0.Not_A_Name"}, `invalid node name "worker-0.Not_A_Name"`},
		{[]string{"power", "on", "worker-["}, "expanding nodes"},
		{[]string{"delete", "worker", ".dev"}, `invalid node name ".dev"`},
		{[]string{"delete", "cluster", "--all", "dev"}, "--all does not accept arguments"},
		{[]string{"logs", "worker-[0-1]"}, "logs requires exactly one node, got 2"},
		{[]string{"ssh"}, "node argument required"},
		{[]string{"ssh", "worker-[0-1]"}, "ssh requires exactly one node, got 2"},
		{[]string{"exec", "--bogus", "--", "true"}, "unknown flag: --bogus"},
		{[]string{"exec", "dev"}, "-- separator and command required"},
		{[]string{"exec", "dev", "--"}, "command required after --"},
		{[]string{"exec", "a", "b", "--", "true"}, "expected at most one argument before --, got 2"},
		{[]string{"exec", "Not_A_Name", "--", "true"}, `invalid cluster name "Not_A_Name"`},
		{[]string{"enter", "a", "b"}, "accepts at most 1 arg(s), received 2"},
		{[]string{"enter", "--user", "1000"}, `invalid user name "1000"`},
		{[]string{"exec", "-u", "Alice", "--", "id"}, `invalid user name "Alice"`},
		{[]string{"ssh", "@worker-0"}, `invalid user name ""`},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer

			code := run(noDocker(t.Context()), tc.args, &stderr)

			assert.Equal(t, exitUsage, code, stderr.String())
			assert.Contains(t, stderr.String(), tc.want)
		})
	}
}

// TestRun_InvalidSindRealmExits1 marks where usage errors end: SIND_REALM
// is not part of the command line, so an invalid one exits 1, like a
// config file that does not validate.
func TestRun_InvalidSindRealmExits1(t *testing.T) {
	t.Setenv("SIND_REALM", "Not_A_Realm")
	var stderr bytes.Buffer

	code := run(noDocker(t.Context()), []string{"get", "clusters"}, &stderr)

	assert.Equal(t, exitFailure, code)
	assert.Contains(t, stderr.String(), `SIND_REALM: invalid realm name "Not_A_Realm"`)
}

func TestUsage(t *testing.T) {
	assert.NoError(t, usage(nil))

	err := fmt.Errorf("parsing: %w", usage(assert.AnError))
	assert.True(t, isUsageError(err))
	require.ErrorIs(t, err, assert.AnError)
	assert.EqualError(t, err, "parsing: "+assert.AnError.Error())

	assert.True(t, isUsageError(usagef("bad %s", "arg")))
	assert.False(t, isUsageError(assert.AnError))
	assert.False(t, isUsageError(nil))
}

func TestNewChildExitError(t *testing.T) {
	for _, tc := range []struct {
		script string
		want   int
	}{
		{"exit 42", 42},
		{"exit 255", 255},
		{"kill -KILL $$", 128 + 9},
		{"kill -HUP $$", 128 + 1},
		{"kill -INT $$", exitInterrupted},
		{"kill -TERM $$", exitInterrupted},
	} {
		t.Run(tc.script, func(t *testing.T) {
			t.Parallel()
			var exitErr *exec.ExitError
			require.ErrorAs(t, exec.Command("sh", "-c", tc.script).Run(), &exitErr)

			err := newChildExitError(exitErr)

			assert.Equal(t, tc.want, err.code)
			assert.Equal(t, exitErr.Error(), err.Error())
			require.ErrorIs(t, err, exitErr)
		})
	}
}

// fakeDockerOnPath puts the fake docker (TestMain) first on PATH, which is
// where dockerExec finds it, and has it end as exit says
// (fakeDockerExitEnv), or hang after it recorded its start in the file it
// returns when exit is empty.
func fakeDockerOnPath(t *testing.T, exit string) string {
	t.Helper()
	dir := t.TempDir()
	self, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, os.Symlink(self, filepath.Join(dir, "docker")))
	started := filepath.Join(dir, "docker-started")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(fakeDockerEnv, started)
	t.Setenv(fakeDockerExitEnv, exit)
	return started
}

// submitterCtx returns a context whose docker client finds the default
// cluster's submitter, the target of exec and enter.
func submitterCtx(ctx context.Context) context.Context {
	var m mock.Executor
	m.AddResult(`{"ID":"abc","Names":"sind-default-submitter","State":"running","Labels":"sind.realm=sind,sind.cluster=default,sind.role=submitter"}`+"\n", "", nil)
	return withClient(ctx, docker.NewClient(&m))
}

// TestRun_PassesTheChildStatusThrough checks that ssh, exec, enter and logs
// exit with the status of the docker command they run, which docker exec
// takes from the command it ran, and that what docker wrote is all that
// stderr gets: they exited 1 and added "exit status N", so a script could
// not tell what the command returned.
func TestRun_PassesTheChildStatusThrough(t *testing.T) {
	commands := map[string][]string{
		"ssh":   {"ssh", "worker-0", "--", "true"},
		"exec":  {"exec", "--", "true"},
		"enter": {"enter"},
		"logs":  {"logs", "worker-0", "slurmd"},
	}
	for _, tc := range []struct {
		exit   string
		want   int
		stderr string
	}{
		{"42", 42, "fake docker exits 42\n"},
		{"255", 255, "fake docker exits 255\n"},
		{"SIGKILL", 128 + 9, ""},
		{"SIGTERM", exitInterrupted, ""},
		{"SIGINT", exitInterrupted, ""},
	} {
		for name, args := range commands {
			t.Run(name+" "+tc.exit, func(t *testing.T) {
				fakeDockerOnPath(t, tc.exit)
				var stderr bytes.Buffer

				code := run(submitterCtx(t.Context()), args, &stderr)

				assert.Equal(t, tc.want, code, stderr.String())
				assert.Equal(t, tc.stderr, stderr.String())
			})
		}
	}
}

// TestRun_InterruptedChildExits130 checks that a program cancelled by an
// interrupt exits 130, not with the status of the SIGKILL that
// exec.CommandContext sends it, and silently, like one that failed.
func TestRun_InterruptedChildExits130(t *testing.T) {
	started := fakeDockerOnPath(t, "")
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(started); err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
	}()
	var stderr bytes.Buffer

	code := run(ctx, []string{"ssh", "worker-0", "--", "sleep", "60"}, &stderr)

	_, err := os.Stat(started)
	require.NoError(t, err, "docker was never started")
	assert.Equal(t, exitInterrupted, code)
	assert.Empty(t, stderr.String())
}

// TestRun_DockerThatCannotStartExits1 checks that a docker that cannot be
// started at all is sind's failure, not a status to pass through.
func TestRun_DockerThatCannotStartExits1(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var stderr bytes.Buffer

	code := run(noDocker(t.Context()), []string{"ssh", "worker-0", "--", "true"}, &stderr)

	assert.Equal(t, exitFailure, code)
	assert.Contains(t, stderr.String(), "executable file not found")
}
