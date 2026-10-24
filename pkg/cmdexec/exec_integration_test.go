// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package cmdexec_test

import (
	"context"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/go-clikit/progress/progresstest"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOSExecutor_SimpleCommand(t *testing.T) {
	var e cmdexec.OSExecutor
	stdout, stderr, err := e.Run(t.Context(), "echo", "hello")
	require.NoError(t, err)
	assert.Equal(t, "hello\n", stdout)
	assert.Empty(t, stderr)
}

// Under a span that shows lines, the lines a command writes reach the
// progress sinks, each with its stream, and the command's output is kept
// as without a span.
func TestOSExecutor_ShowsLines(t *testing.T) {
	ctx, w := progresstest.Watch(t.Context(), t)
	ctx, step := progress.Start(ctx, progress.KindStep, "step", progress.Flagged(progress.FlagShowLines))
	var e cmdexec.OSExecutor
	stdout, stderr, err := e.Run(ctx, "sh", "-c", "echo out; echo err >&2")
	step.End(err)
	require.NoError(t, err)
	assert.Equal(t, "out\n", stdout)
	assert.Equal(t, "err\n", stderr)
	var lines []string
	for _, ev := range w.Events() {
		if ev.Type == progress.TypeLine {
			lines = append(lines, ev.Stream.String()+" "+ev.Text)
		}
	}
	assert.ElementsMatch(t, []string{"stdout out", "stderr err"}, lines)
	assert.Equal(t, "step step [show-lines]: ok\n", w.Finish())
}

// A command whose standard output is data shows the lines of its standard
// error alone, and its output is kept as without a span.
func TestOSExecutor_ShowsNoLinesOfData(t *testing.T) {
	ctx, w := progresstest.Watch(t.Context(), t)
	ctx, step := progress.Start(ctx, progress.KindStep, "step", progress.Flagged(progress.FlagShowLines))
	var e cmdexec.OSExecutor
	stdout, stderr, err := e.Run(cmdexec.WithStdoutAsData(ctx), "sh", "-c", "echo '[{\"Id\": 1}]'; echo pulling >&2")
	step.End(err)
	require.NoError(t, err)
	assert.Equal(t, "[{\"Id\": 1}]\n", stdout)
	assert.Equal(t, "pulling\n", stderr)
	var lines []string
	for _, ev := range w.Events() {
		if ev.Type == progress.TypeLine {
			lines = append(lines, ev.Stream.String()+" "+ev.Text)
		}
	}
	assert.Equal(t, []string{"stderr pulling"}, lines)
	w.Finish()
}

func TestOSExecutor_CapturesStderr(t *testing.T) {
	var e cmdexec.OSExecutor
	stdout, stderr, err := e.Run(t.Context(), "sh", "-c", "echo error >&2")
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Equal(t, "error\n", stderr)
}

func TestOSExecutor_ExitError(t *testing.T) {
	var e cmdexec.OSExecutor
	_, _, err := e.Run(t.Context(), "sh", "-c", "exit 1")
	require.Error(t, err)
	var exitErr *exec.ExitError
	assert.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.ExitCode())
}

func TestOSExecutor_ExitErrorPreservesOutput(t *testing.T) {
	var e cmdexec.OSExecutor
	stdout, stderr, err := e.Run(t.Context(), "sh", "-c", "echo out; echo err >&2; exit 2")
	require.Error(t, err)
	assert.Equal(t, "out\n", stdout)
	assert.Equal(t, "err\n", stderr)
}

func TestOSExecutor_ExitErrorCarriesStderr(t *testing.T) {
	var e cmdexec.OSExecutor
	_, stderr, err := e.Run(t.Context(), "sh", "-c", "echo 'Error: No such container: x' >&2; exit 1")
	require.EqualError(t, err, "exit status 1: Error: No such container: x")
	assert.Equal(t, "Error: No such container: x\n", stderr)
	var exitErr *cmdexec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.ExitCode())
	assert.Equal(t, stderr, exitErr.Stderr)
}

func TestOSExecutor_CommandNotFound(t *testing.T) {
	var e cmdexec.OSExecutor
	_, _, err := e.Run(t.Context(), "nonexistent-command-xyz")
	require.Error(t, err)
}

func TestOSExecutor_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var e cmdexec.OSExecutor
	_, _, err := e.Run(ctx, "sleep", "10")
	require.Error(t, err)
}

func TestOSExecutor_WithStdin(t *testing.T) {
	var e cmdexec.OSExecutor
	stdin := strings.NewReader("hello from stdin")
	stdout, stderr, err := e.RunWithStdin(t.Context(), stdin, "cat")
	require.NoError(t, err)
	assert.Equal(t, "hello from stdin", stdout)
	assert.Empty(t, stderr)
}

// A line parser is handed the lines of both streams as they end, without
// their line breaks, with no span to show them under; the output is kept as
// without one. Standard output that is data is not parsed.
func TestOSExecutor_ParsesLines(t *testing.T) {
	var e cmdexec.OSExecutor
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want []string
	}{
		{"both", t.Context(), []string{"stderr err", "stdout out"}},
		{"data", cmdexec.WithStdoutAsData(t.Context()), []string{"stderr err"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var lines []string
			ctx := cmdexec.WithLineParser(tc.ctx, func(st progress.Stream, line string) {
				mu.Lock()
				defer mu.Unlock()
				lines = append(lines, st.String()+" "+line)
			})

			stdout, stderr, err := e.Run(ctx, "sh", "-c", "printf 'out\\r\\nlast'; echo err >&2")

			require.NoError(t, err)
			assert.Equal(t, "out\r\nlast", stdout)
			assert.Equal(t, "err\n", stderr)
			mu.Lock()
			defer mu.Unlock()
			slices.Sort(lines)
			assert.Equal(t, tc.want, lines)
		})
	}
}

// A command runs with the environment WithEnv gives it, and WithStarted is
// told its process ID while it runs.
func TestOSExecutor_EnvAndStarted(t *testing.T) {
	var e cmdexec.OSExecutor
	var pid int
	ctx := cmdexec.WithEnv(t.Context(), []string{"ONLY=this"})
	ctx = cmdexec.WithStarted(ctx, func(p int) { pid = p })
	stdout, _, err := e.Run(ctx, "/bin/sh", "-c", "echo $ONLY $$; echo ${HOME:-nohome}")
	require.NoError(t, err)
	assert.Positive(t, pid)
	assert.Equal(t, "this "+strconv.Itoa(pid)+"\nnohome\n", stdout)
}
