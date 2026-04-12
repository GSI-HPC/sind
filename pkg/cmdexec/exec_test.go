// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- MockExecutor ---

type failReader struct{}

func (r *failReader) Read([]byte) (int, error) {
	return 0, fmt.Errorf("read failed")
}

func TestMockExecutor_RecordsCalls(t *testing.T) {
	var m mock.Executor
	m.AddResult("ok\n", "", nil)
	m.AddResult("", "", nil)

	_, _, _ = m.Run(t.Context(), "docker", "ps")
	_, _, _ = m.Run(t.Context(), "docker", "run", "--rm", "alpine")

	require.Len(t, m.Calls, 2)
	assert.Equal(t, mock.Call{Name: "docker", Args: []string{"ps"}}, m.Calls[0])
	assert.Equal(t, mock.Call{Name: "docker", Args: []string{"run", "--rm", "alpine"}}, m.Calls[1])
}

func TestMockExecutor_ReturnsResults(t *testing.T) {
	var m mock.Executor
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
	var m mock.Executor
	_, _, err := m.Run(t.Context(), "docker", "ps")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected call")
}

func TestMockExecutor_WithStdinError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)

	r := &failReader{}
	_, _, err := m.RunWithStdin(t.Context(), r, "cmd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mock: reading stdin")
}

func TestMockExecutor_OnCall(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(_ []string, _ string) mock.Result {
		return mock.Result{Stdout: "dispatched", Stderr: "", Err: nil}
	}

	stdout, _, err := m.Run(t.Context(), "docker", "ps")
	require.NoError(t, err)
	assert.Equal(t, "dispatched", stdout)
	require.Len(t, m.Calls, 1)
}

func TestMockExecutor_WithStdin(t *testing.T) {
	var m mock.Executor
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
	var m mock.Executor
	m.AddResult("out", "", nil)
	rec := &mock.RecordingExecutor{Inner: &m}

	_, _, _ = rec.Run(t.Context(), "docker", "ps")

	calls := rec.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "docker", calls[0].Name)
	assert.Equal(t, []string{"ps"}, calls[0].Args)
}

func TestRecordingExecutor_Dump(t *testing.T) {
	var m mock.Executor
	m.AddResult("out", "warn", fmt.Errorf("fail"))
	rec := &mock.RecordingExecutor{Inner: &m}

	_, _, _ = rec.Run(t.Context(), "docker", "ps")

	dump := rec.Dump()
	assert.Contains(t, dump, "docker ps")
	assert.Contains(t, dump, "stdout: out")
	assert.Contains(t, dump, "stderr: warn")
	assert.Contains(t, dump, "err: fail")
}

func TestRecordingExecutor_RunWithStdin(t *testing.T) {
	var m mock.Executor
	m.AddResult("ok", "", nil)
	rec := &mock.RecordingExecutor{Inner: &m}

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

func TestNewRecorder(t *testing.T) {
	rec := mock.NewRecorder()
	assert.False(t, rec.IsIntegration())

	rec.AddResult("out", "", nil)
	stdout, _, err := rec.Run(t.Context(), "cmd")
	require.NoError(t, err)
	assert.Equal(t, "out", stdout)
}

func TestNewIntegrationRecorder(t *testing.T) {
	rec := mock.NewIntegrationRecorder()
	assert.True(t, rec.IsIntegration())
}
