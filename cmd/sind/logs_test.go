// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/docker"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogs_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"logs"})
	require.NoError(t, err)
	assert.Equal(t, "logs NODE [SERVICE]", c.Use)
}

func TestLogs_RequiresArgs(t *testing.T) {
	_, _, err := executeCommand("logs")
	assert.Error(t, err)
}

func TestLogs_TooManyArgs(t *testing.T) {
	_, _, err := executeCommand("logs", "a", "b", "c")
	assert.Error(t, err)
}

func TestLogs_HasFollowFlag(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"logs"})
	require.NoError(t, err)
	f := c.Flags().Lookup("follow")
	require.NotNil(t, f)
	assert.Equal(t, "f", f.Shorthand)
}

// TestLogs_MissingNode checks that logs, like ssh, names the missing cluster
// or node rather than passing docker's error, with an internal container
// name, through; docker never runs.
func TestLogs_MissingNode(t *testing.T) {
	for _, command := range [][]string{{"logs", "worker-9"}, {"ssh", "worker-9", "--", "true"}} {
		t.Run(command[0]+" node", func(t *testing.T) {
			fakeDockerOnPath(t, "0")
			var stderr bytes.Buffer

			code := run(submitterCtx(t.Context()), command, &stderr)

			assert.Equal(t, exitFailure, code)
			assert.Contains(t, stderr.String(), `node "worker-9" not found in cluster "default"`)
			assert.NotContains(t, stderr.String(), "sind-default")
		})
		t.Run(command[0]+" cluster", func(t *testing.T) {
			fakeDockerOnPath(t, "0")
			var stderr bytes.Buffer

			emptyRealm := &mock.Executor{OnCall: func([]string, string) mock.Result { return mock.Result{} }}
			ctx := withClient(t.Context(), docker.NewClient(emptyRealm))

			code := run(ctx, append([]string{command[0], "worker-9.nosuch"}, command[2:]...), &stderr)

			assert.Equal(t, exitFailure, code)
			assert.Contains(t, stderr.String(), `cluster "nosuch" not found in realm "sind"`)
		})
	}
}
