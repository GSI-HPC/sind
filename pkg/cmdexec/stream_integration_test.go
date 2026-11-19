// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package cmdexec_test

import (
	"bufio"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStart(t *testing.T) {
	proc, err := cmdexec.Start(t.Context(), "echo", "hello")
	require.NoError(t, err)
	defer func() { _ = proc.Close() }()

	scanner := bufio.NewScanner(proc.Stdout)
	require.True(t, scanner.Scan(), "expected output line")
	assert.Equal(t, "hello", scanner.Text())
}

// A started command reads a stdin that stays open until Close.
func TestStart_StdinStaysOpen(t *testing.T) {
	proc, err := cmdexec.Start(t.Context(), "sh", "-c", "echo started; cat; echo stdin ended")
	require.NoError(t, err)
	lines := make(chan string, 2)
	go func() {
		scanner := bufio.NewScanner(proc.Stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	assert.Equal(t, "started", <-lines)
	select {
	case line := <-lines:
		t.Fatalf("got %q while the process runs", line)
	case <-time.After(300 * time.Millisecond):
	}
	require.NoError(t, proc.Close())
}

func TestStart_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	proc, err := cmdexec.Start(ctx, "sleep", "60")
	require.NoError(t, err)

	cancel()
	// Close kills a running process; the "signal: killed" ExitError
	// from the intentional Kill should be suppressed.
	assert.NoError(t, proc.Close())
}

func TestProcess_Close_KillsRunningProcess(t *testing.T) {
	proc, err := cmdexec.Start(t.Context(), "sleep", "60")
	require.NoError(t, err)
	// Close should kill the running process and return nil, not
	// "signal: killed".
	assert.NoError(t, proc.Close())
}

func TestStart_InvalidCommand(t *testing.T) {
	_, err := cmdexec.Start(t.Context(), "/nonexistent-binary-xyz")
	require.Error(t, err)
}

func TestProcess_Close_WithCmd(t *testing.T) {
	proc, err := cmdexec.Start(t.Context(), "echo", "done")
	require.NoError(t, err)
	_ = proc.Close()
}

func TestOSExecutor_Start(t *testing.T) {
	var e cmdexec.OSExecutor
	proc, err := e.Start(t.Context(), "echo", "hello")
	require.NoError(t, err)
	defer func() { _ = proc.Close() }()

	scanner := bufio.NewScanner(proc.Stdout)
	require.True(t, scanner.Scan(), "expected output line")
	assert.Equal(t, "hello", scanner.Text())
}

func TestStart_StderrMerged(t *testing.T) {
	ctx := cmdexec.WithStderrMerged(t.Context())
	proc, err := cmdexec.Start(ctx, "sh", "-c", "echo out; echo err >&2")
	require.NoError(t, err)
	defer func() { _ = proc.Close() }()

	var lines []string
	sc := bufio.NewScanner(proc.Stdout)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	assert.ElementsMatch(t, []string{"out", "err"}, lines)
}

func TestStart_StderrMergedNotFound(t *testing.T) {
	_, err := cmdexec.Start(cmdexec.WithStderrMerged(t.Context()), "/nonexistent/command")
	assert.Error(t, err)
}

// Close releases Stdout in both modes of Start: a read after Close fails
// as on a closed file, also under WithStderrMerged, whose pipe Wait does
// not close.
func TestProcess_Close_ClosesStdout(t *testing.T) {
	for _, tt := range []struct {
		name string
		ctx  context.Context
	}{
		{"pipe", t.Context()},
		{"merged", cmdexec.WithStderrMerged(t.Context())},
	} {
		t.Run(tt.name, func(t *testing.T) {
			proc, err := cmdexec.Start(tt.ctx, "sleep", "60")
			require.NoError(t, err)
			require.NoError(t, proc.Close())
			_, err = proc.Stdout.Read(make([]byte, 1))
			assert.ErrorIs(t, err, os.ErrClosed)
			assert.NotErrorIs(t, err, io.EOF)
		})
	}
}

// Close of a command that failed by itself returns an *ExitError with its
// stderr, which says why the stream ended.
func TestProcess_Close_KeepsStderr(t *testing.T) {
	proc, err := cmdexec.Start(t.Context(), "sh", "-c", "echo out; echo 'Cannot connect to the Docker daemon' >&2; exit 1")
	require.NoError(t, err)
	out, err := io.ReadAll(proc.Stdout)
	require.NoError(t, err)
	assert.Equal(t, "out\n", string(out))

	err = proc.Close()
	var exitErr *cmdexec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.ExitCode())
	assert.Equal(t, "Cannot connect to the Docker daemon\n", exitErr.Stderr)
	assert.Same(t, err, proc.Close(), "a second Close returns the first one's result")
}

// Close keeps the head and the tail of a long stderr.
func TestProcess_Close_BoundsStderr(t *testing.T) {
	proc, err := cmdexec.Start(t.Context(), "sh", "-c", "echo first >&2; head -c 100000 /dev/zero | tr '\\0' x >&2; echo; echo last >&2; exit 3")
	require.NoError(t, err)
	_, _ = io.ReadAll(proc.Stdout)
	var exitErr *cmdexec.ExitError
	require.ErrorAs(t, proc.Close(), &exitErr)
	assert.Less(t, len(exitErr.Stderr), 5000)
	assert.True(t, strings.HasPrefix(exitErr.Stderr, "first\n"), exitErr.Stderr[:10])
	assert.True(t, strings.HasSuffix(exitErr.Stderr, "last\n"))
	assert.Contains(t, exitErr.Stderr, "bytes truncated")
}

// A command that Close kills is no error.
func TestProcess_Close_KilledIsNoError(t *testing.T) {
	proc, err := cmdexec.Start(t.Context(), "sh", "-c", "echo err >&2; exec sleep 60")
	require.NoError(t, err)
	assert.NoError(t, proc.Close())
}

// Close of a command that left a process behind that holds its stderr
// returns after outputWaitDelay, without an error for a command that
// exited 0.
func TestProcess_Close_LeftBehindHoldsStderr(t *testing.T) {
	proc, err := cmdexec.Start(t.Context(), "sh", "-c", "sleep 30 >/dev/null </dev/null & exit 0")
	require.NoError(t, err)
	_, _ = io.ReadAll(proc.Stdout)
	start := time.Now()
	assert.NoError(t, proc.Close())
	assert.Less(t, time.Since(start), 4*time.Second)
}
