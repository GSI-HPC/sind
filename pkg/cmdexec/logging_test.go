// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoggingExecutor_Run(t *testing.T) {
	var m MockExecutor
	m.AddResult("ok", "", nil)

	var logged string
	l := &LoggingExecutor{
		Inner: &m,
		Log:   func(_ context.Context, cmd string) { logged = cmd },
	}

	stdout, _, err := l.Run(t.Context(), "resolvectl", "dns", "br-abc", "172.18.0.2")
	require.NoError(t, err)
	assert.Equal(t, "ok", stdout)
	assert.Equal(t, "resolvectl dns br-abc 172.18.0.2", logged)
}

func TestLoggingExecutor_RunWithStdin(t *testing.T) {
	var m MockExecutor
	m.AddResult("ok", "", nil)

	var logged string
	l := &LoggingExecutor{
		Inner: &m,
		Log:   func(_ context.Context, cmd string) { logged = cmd },
	}

	stdin := strings.NewReader("data")
	stdout, _, err := l.RunWithStdin(t.Context(), stdin, "cmd", "arg")
	require.NoError(t, err)
	assert.Equal(t, "ok", stdout)
	assert.Equal(t, "cmd arg", logged)
}
