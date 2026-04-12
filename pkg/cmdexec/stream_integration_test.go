// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package cmdexec_test

import (
	"bufio"
	"context"
	"testing"

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
