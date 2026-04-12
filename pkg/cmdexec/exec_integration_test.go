// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package cmdexec_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"

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
