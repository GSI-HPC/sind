// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"testing"

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
