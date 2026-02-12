// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"fmt"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOSExecutor_SimpleCommand(t *testing.T) {
	var e OSExecutor
	stdout, stderr, err := e.Run(context.Background(), "echo", "hello")
	require.NoError(t, err)
	assert.Equal(t, "hello\n", stdout)
	assert.Empty(t, stderr)
}

func TestOSExecutor_CapturesStderr(t *testing.T) {
	var e OSExecutor
	stdout, stderr, err := e.Run(context.Background(), "sh", "-c", "echo error >&2")
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Equal(t, "error\n", stderr)
}

func TestOSExecutor_ExitError(t *testing.T) {
	var e OSExecutor
	_, _, err := e.Run(context.Background(), "sh", "-c", "exit 1")
	require.Error(t, err)
	var exitErr *exec.ExitError
	assert.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.ExitCode())
}

func TestOSExecutor_ExitErrorPreservesOutput(t *testing.T) {
	var e OSExecutor
	stdout, stderr, err := e.Run(context.Background(), "sh", "-c", "echo out; echo err >&2; exit 2")
	require.Error(t, err)
	assert.Equal(t, "out\n", stdout)
	assert.Equal(t, "err\n", stderr)
}

func TestOSExecutor_CommandNotFound(t *testing.T) {
	var e OSExecutor
	_, _, err := e.Run(context.Background(), "nonexistent-command-xyz")
	require.Error(t, err)
}

func TestOSExecutor_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var e OSExecutor
	_, _, err := e.Run(ctx, "sleep", "10")
	require.Error(t, err)
}

func TestMockExecutor_RecordsCalls(t *testing.T) {
	var m MockExecutor
	m.AddResult("ok\n", "", nil)
	m.AddResult("", "", nil)

	m.Run(context.Background(), "docker", "ps")
	m.Run(context.Background(), "docker", "run", "--rm", "alpine")

	require.Len(t, m.Calls, 2)
	assert.Equal(t, MockCall{Name: "docker", Args: []string{"ps"}}, m.Calls[0])
	assert.Equal(t, MockCall{Name: "docker", Args: []string{"run", "--rm", "alpine"}}, m.Calls[1])
}

func TestMockExecutor_ReturnsResults(t *testing.T) {
	var m MockExecutor
	m.AddResult("out1", "err1", nil)
	m.AddResult("out2", "", fmt.Errorf("fail"))

	stdout, stderr, err := m.Run(context.Background(), "cmd1")
	assert.Equal(t, "out1", stdout)
	assert.Equal(t, "err1", stderr)
	assert.NoError(t, err)

	stdout, stderr, err = m.Run(context.Background(), "cmd2")
	assert.Equal(t, "out2", stdout)
	assert.Empty(t, stderr)
	assert.EqualError(t, err, "fail")
}

func TestMockExecutor_UnexpectedCall(t *testing.T) {
	var m MockExecutor
	_, _, err := m.Run(context.Background(), "docker", "ps")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected call")
}
