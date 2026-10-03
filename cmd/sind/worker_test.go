// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateWorker_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"create", "worker"})
	require.NoError(t, err)
	assert.Equal(t, "worker [CLUSTER]", c.Use)
}

func TestCreateWorker_Flags(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"create", "worker"})
	require.NoError(t, err)

	flags := []string{"count", "image", "cpus", "memory", "tmp-size", "unmanaged"}
	for _, f := range flags {
		assert.NotNil(t, c.Flags().Lookup(f), "missing flag: %s", f)
	}
}

func TestCreateWorker_InvalidFlags(t *testing.T) {
	// Flag values create worker cannot act on are usage errors, reported
	// before it takes the realm lock or calls docker.
	tests := []struct {
		args    []string
		wantErr string
	}{
		{[]string{"--count", "0"}, "--count must be at least 1, got 0"},
		{[]string{"--count", "-3"}, "--count must be at least 1, got -3"},
		{[]string{"--cpus", "-1"}, "--cpus must not be negative, got -1"},
		{[]string{"--pull"}, "--pull needs --image"},
		{[]string{"--cap-add", "BOGUS"}, `unknown capability "BOGUS" in --cap-add`},
		{[]string{"--cap-drop", "net_raw"}, `unknown capability "net_raw" in --cap-drop`},
		{[]string{"--device", "relative/dev"}, `device path must be absolute, got "relative/dev"`},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var m mock.Executor

			_, _, err := executeWithMock(&m, append([]string{"create", "worker"}, tt.args...)...)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.True(t, isUsageError(err), "exits 2")
			assert.Empty(t, m.Calls, "no docker call")
		})
	}
}

func TestCreateWorker_FlagHelpNamesDefaults(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"create", "worker"})
	require.NoError(t, err)

	assert.Equal(t, "CPU limit per node (default: the newest worker's, else 1)", c.Flags().Lookup("cpus").Usage)
	assert.Equal(t, "memory limit per node (default: the newest worker's, else 512m)", c.Flags().Lookup("memory").Usage)
	assert.Equal(t, "/tmp tmpfs size (default: the newest worker's, else 256m)", c.Flags().Lookup("tmp-size").Usage)
	assert.Contains(t, c.Flags().Lookup("image").Usage, "the newest worker's, else the controller's")
}

func TestCreateWorker_TooManyArgs(t *testing.T) {
	_, _, err := executeCommand("create", "worker", "a", "b")
	assert.Error(t, err)
}

func TestDeleteWorker_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"delete", "worker"})
	require.NoError(t, err)
	assert.Equal(t, "worker NODES", c.Use)
}

func TestDeleteWorker_RequiresArgs(t *testing.T) {
	_, _, err := executeCommand("delete", "worker")
	assert.Error(t, err)
}
