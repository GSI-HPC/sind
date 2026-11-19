// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package cmdexec_test

import (
	"bufio"
	"context"
	"io"
	"os"
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
