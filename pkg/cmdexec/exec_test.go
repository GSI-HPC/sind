// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- OSExecutor ---

func TestOSExecutor_SimpleCommand(t *testing.T) {
	var e OSExecutor
	stdout, stderr, err := e.Run(t.Context(), "echo", "hello")
	require.NoError(t, err)
	assert.Equal(t, "hello\n", stdout)
	assert.Empty(t, stderr)
}

func TestOSExecutor_CapturesStderr(t *testing.T) {
	var e OSExecutor
	stdout, stderr, err := e.Run(t.Context(), "sh", "-c", "echo error >&2")
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Equal(t, "error\n", stderr)
}

func TestOSExecutor_ExitError(t *testing.T) {
	var e OSExecutor
	_, _, err := e.Run(t.Context(), "sh", "-c", "exit 1")
	require.Error(t, err)
	var exitErr *exec.ExitError
	assert.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.ExitCode())
}

func TestOSExecutor_ExitErrorPreservesOutput(t *testing.T) {
	var e OSExecutor
	stdout, stderr, err := e.Run(t.Context(), "sh", "-c", "echo out; echo err >&2; exit 2")
	require.Error(t, err)
	assert.Equal(t, "out\n", stdout)
	assert.Equal(t, "err\n", stderr)
}

func TestOSExecutor_CommandNotFound(t *testing.T) {
	var e OSExecutor
	_, _, err := e.Run(t.Context(), "nonexistent-command-xyz")
	require.Error(t, err)
}

func TestOSExecutor_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var e OSExecutor
	_, _, err := e.Run(ctx, "sleep", "10")
	require.Error(t, err)
}

func TestOSExecutor_WithStdin(t *testing.T) {
	var e OSExecutor
	stdin := strings.NewReader("hello from stdin")
	stdout, stderr, err := e.RunWithStdin(t.Context(), stdin, "cat")
	require.NoError(t, err)
	assert.Equal(t, "hello from stdin", stdout)
	assert.Empty(t, stderr)
}

// --- MockExecutor ---

type failReader struct{}

func (r *failReader) Read([]byte) (int, error) {
	return 0, fmt.Errorf("read failed")
}

func TestMockExecutor_RecordsCalls(t *testing.T) {
	var m MockExecutor
	m.AddResult("ok\n", "", nil)
	m.AddResult("", "", nil)

	_, _, _ = m.Run(t.Context(), "docker", "ps")
	_, _, _ = m.Run(t.Context(), "docker", "run", "--rm", "alpine")

	require.Len(t, m.Calls, 2)
	assert.Equal(t, MockCall{Name: "docker", Args: []string{"ps"}}, m.Calls[0])
	assert.Equal(t, MockCall{Name: "docker", Args: []string{"run", "--rm", "alpine"}}, m.Calls[1])
}

func TestMockExecutor_ReturnsResults(t *testing.T) {
	var m MockExecutor
	m.AddResult("out1", "err1", nil)
	m.AddResult("out2", "", fmt.Errorf("fail"))

	stdout, stderr, err := m.Run(t.Context(), "cmd1")
	assert.Equal(t, "out1", stdout)
	assert.Equal(t, "err1", stderr)
	assert.NoError(t, err)

	stdout, stderr, err = m.Run(t.Context(), "cmd2")
	assert.Equal(t, "out2", stdout)
	assert.Empty(t, stderr)
	assert.EqualError(t, err, "fail")
}

func TestMockExecutor_UnexpectedCall(t *testing.T) {
	var m MockExecutor
	_, _, err := m.Run(t.Context(), "docker", "ps")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected call")
}

func TestMockExecutor_WithStdinError(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "", nil)

	r := &failReader{}
	_, _, err := m.RunWithStdin(t.Context(), r, "cmd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mock: reading stdin")
}

func TestMockExecutor_OnCall(t *testing.T) {
	var m MockExecutor
	m.OnCall = func(_ []string, _ string) MockResult {
		return MockResult{Stdout: "dispatched", Stderr: "", Err: nil}
	}

	stdout, _, err := m.Run(t.Context(), "docker", "ps")
	require.NoError(t, err)
	assert.Equal(t, "dispatched", stdout)
	require.Len(t, m.Calls, 1)
}

func TestMockExecutor_WithStdin(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "", nil)

	stdin := strings.NewReader("key-data")
	_, _, _ = m.RunWithStdin(t.Context(), stdin, "docker", "exec", "-i", "node")

	require.Len(t, m.Calls, 1)
	assert.Equal(t, "docker", m.Calls[0].Name)
	assert.Equal(t, []string{"exec", "-i", "node"}, m.Calls[0].Args)
	assert.Equal(t, "key-data", m.Calls[0].Stdin)
}

// --- RecordingExecutor ---

func TestRecordingExecutor_Calls(t *testing.T) {
	var m MockExecutor
	m.AddResult("out", "", nil)
	rec := &RecordingExecutor{Inner: &m}

	_, _, _ = rec.Run(t.Context(), "docker", "ps")

	calls := rec.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "docker", calls[0].Name)
	assert.Equal(t, []string{"ps"}, calls[0].Args)
}

func TestRecordingExecutor_Dump(t *testing.T) {
	var m MockExecutor
	m.AddResult("out", "warn", fmt.Errorf("fail"))
	rec := &RecordingExecutor{Inner: &m}

	_, _, _ = rec.Run(t.Context(), "docker", "ps")

	dump := rec.Dump()
	assert.Contains(t, dump, "docker ps")
	assert.Contains(t, dump, "stdout: out")
	assert.Contains(t, dump, "stderr: warn")
	assert.Contains(t, dump, "err: fail")
}

func TestRecordingExecutor_RunWithStdin(t *testing.T) {
	var m MockExecutor
	m.AddResult("ok", "", nil)
	rec := &RecordingExecutor{Inner: &m}

	stdin := strings.NewReader("input")
	stdout, _, err := rec.RunWithStdin(t.Context(), stdin, "cmd", "arg")
	require.NoError(t, err)
	assert.Equal(t, "ok", stdout)

	calls := rec.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "cmd", calls[0].Name)
	assert.Equal(t, []string{"arg"}, calls[0].Args)
}

// --- Recorder ---

func TestNewMockRecorder(t *testing.T) {
	rec := NewMockRecorder()
	assert.False(t, rec.IsIntegration())

	rec.AddResult("out", "", nil)
	stdout, _, err := rec.Run(t.Context(), "cmd")
	require.NoError(t, err)
	assert.Equal(t, "out", stdout)
}

func TestNewIntegrationRecorder(t *testing.T) {
	rec := NewIntegrationRecorder()
	assert.True(t, rec.IsIntegration())
}
