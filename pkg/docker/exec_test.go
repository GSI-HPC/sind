// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failReader struct{}

func (r *failReader) Read([]byte) (int, error) {
	return 0, fmt.Errorf("read failed")
}

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

func TestOSExecutor_WithStdin(t *testing.T) {
	var e OSExecutor
	stdin := strings.NewReader("hello from stdin")
	stdout, stderr, err := e.RunWithStdin(context.Background(), stdin, "cat")
	require.NoError(t, err)
	assert.Equal(t, "hello from stdin", stdout)
	assert.Empty(t, stderr)
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

func TestMockExecutor_WithStdinError(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "", nil)

	r := &failReader{}
	_, _, err := m.RunWithStdin(context.Background(), r, "cmd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mock: reading stdin")
}

func TestMockExecutor_OnCall(t *testing.T) {
	var m MockExecutor
	m.OnCall = func(args []string, stdin string) MockResult {
		return MockResult{Stdout: "dispatched", Stderr: "", Err: nil}
	}

	stdout, _, err := m.Run(context.Background(), "docker", "ps")
	require.NoError(t, err)
	assert.Equal(t, "dispatched", stdout)
	require.Len(t, m.Calls, 1)
}

func TestMockExecutor_WithStdin(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "", nil)

	stdin := strings.NewReader("key-data")
	m.RunWithStdin(context.Background(), stdin, "docker", "exec", "-i", "node")

	require.Len(t, m.Calls, 1)
	assert.Equal(t, "docker", m.Calls[0].Name)
	assert.Equal(t, []string{"exec", "-i", "node"}, m.Calls[0].Args)
	assert.Equal(t, "key-data", m.Calls[0].Stdin)
}
