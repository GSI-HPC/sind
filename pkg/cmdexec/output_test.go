// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec

import (
	"testing"

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

func TestInProcessGroup(t *testing.T) {
	assert.False(t, InProcessGroup(t.Context()))
	assert.True(t, InProcessGroup(WithProcessGroup(t.Context())))
}

func TestLineParser(t *testing.T) {
	assert.Nil(t, LineParser(t.Context()))

	var got []string
	ctx := WithLineParser(t.Context(), func(st progress.Stream, line string) { got = append(got, st.String()+" "+line) })
	parse := LineParser(ctx)
	require.NotNil(t, parse)
	parse(progress.Stdout, "a3ed95caeb02: Pull complete")
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
