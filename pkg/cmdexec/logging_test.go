// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoggingExecutor_Run(t *testing.T) {
	var m mock.Executor
	m.AddResult("ok", "", nil)

	var logged string
	l := &cmdexec.LoggingExecutor{
		Inner: &m,
		Log:   func(_ context.Context, cmd string) { logged = cmd },
	}

	stdout, _, err := l.Run(t.Context(), "resolvectl", "dns", "br-abc", "172.18.0.2")
	require.NoError(t, err)
	assert.Equal(t, "ok", stdout)
	assert.Equal(t, "resolvectl dns br-abc 172.18.0.2", logged)
}

func TestLoggingExecutor_RunWithStdin(t *testing.T) {
	var m mock.Executor
	m.AddResult("ok", "", nil)

	var logged string
	l := &cmdexec.LoggingExecutor{
		Inner: &m,
		Log:   func(_ context.Context, cmd string) { logged = cmd },
	}

	stdin := strings.NewReader("data")
	stdout, _, err := l.RunWithStdin(t.Context(), stdin, "cmd", "arg")
	require.NoError(t, err)
	assert.Equal(t, "ok", stdout)
	assert.Equal(t, "cmd arg", logged)
}

func TestLoggingExecutor_Start(t *testing.T) {
	inner := &mock.Executor{
		OnStart: func(_ []string) mock.StreamResult {
			pr, pw := io.Pipe()
			t.Cleanup(func() { _ = pw.Close() })
			return mock.StreamResult{Reader: pr}
		},
	}

	var logged string
	l := &cmdexec.LoggingExecutor{
		Inner: inner,
		Log:   func(_ context.Context, cmd string) { logged = cmd },
	}

	proc, err := l.Start(t.Context(), "docker", "events")
	require.NoError(t, err)
	_ = proc.Close()

	assert.Equal(t, "docker events", logged)
}
