// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordingExecutor_Calls(t *testing.T) {
	var m MockExecutor
	m.AddResult("out", "", nil)
	rec := &RecordingExecutor{Inner: &m}

	rec.Run(t.Context(), "docker", "ps")

	calls := rec.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "docker", calls[0].Name)
	assert.Equal(t, []string{"ps"}, calls[0].Args)
}

func TestRecordingExecutor_Dump(t *testing.T) {
	var m MockExecutor
	m.AddResult("out", "warn", fmt.Errorf("fail"))
	rec := &RecordingExecutor{Inner: &m}

	rec.Run(t.Context(), "docker", "ps")

	dump := rec.Dump()
	assert.Contains(t, dump, "docker ps")
	assert.Contains(t, dump, "stdout: out")
	assert.Contains(t, dump, "stderr: warn")
	assert.Contains(t, dump, "err: fail")
}

func TestNewIntegrationRecorder(t *testing.T) {
	rec := NewIntegrationRecorder()
	assert.True(t, rec.IsIntegration())
}
