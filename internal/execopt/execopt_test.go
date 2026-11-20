// SPDX-License-Identifier: LGPL-3.0-or-later

package execopt

import (
	"testing"
	"time"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStdoutIsData(t *testing.T) {
	assert.False(t, StdoutIsData(t.Context()))
	assert.True(t, StdoutIsData(WithStdoutAsData(t.Context())))
}

func TestStderrMerged(t *testing.T) {
	assert.False(t, StderrMerged(t.Context()))
	assert.True(t, StderrMerged(WithStderrMerged(t.Context())))
}

func TestStopGrace(t *testing.T) {
	assert.Zero(t, StopGrace(t.Context()))
	assert.Equal(t, time.Minute, StopGrace(WithStopGrace(t.Context(), time.Minute)))
}

func TestInProcessGroup(t *testing.T) {
	assert.False(t, InProcessGroup(t.Context()))
	assert.True(t, InProcessGroup(WithProcessGroup(t.Context())))
}

func TestOrphanSafe(t *testing.T) {
	assert.False(t, OrphanSafe(t.Context()))
	assert.True(t, OrphanSafe(WithOrphanSafe(t.Context())))
}

func TestLineParser(t *testing.T) {
	assert.Nil(t, LineParser(t.Context()))

	var got []string
	ctx := WithLineParser(t.Context(), func(st progress.Stream, line string) { got = append(got, st.String()+" "+line) })
	parse := LineParser(ctx)
	require.NotNil(t, parse)
	parse(progress.StreamStdout, "a3ed95caeb02: Pull complete")
	assert.Equal(t, []string{"stdout a3ed95caeb02: Pull complete"}, got)
}

func TestEnv(t *testing.T) {
	assert.Nil(t, Env(t.Context()))
	assert.Equal(t, []string{"A=1"}, Env(WithEnv(t.Context(), []string{"A=1"})))
}

func TestStarted(t *testing.T) {
	assert.Nil(t, Started(t.Context()))

	var pid int
	started := Started(WithStarted(t.Context(), func(p int) { pid = p }))
	require.NotNil(t, started)
	started(42)
	assert.Equal(t, 42, pid)
}

func TestOutputLimit(t *testing.T) {
	assert.Zero(t, OutputLimit(t.Context()))
	assert.Equal(t, 64, OutputLimit(WithOutputLimit(t.Context(), 64)))
}
