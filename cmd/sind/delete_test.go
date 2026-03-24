// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeleteCluster_DefaultName(t *testing.T) {
	cmd := NewRootCommand()
	deleteCmd, _, err := cmd.Find([]string{"delete", "cluster"})
	require.NoError(t, err)
	assert.Equal(t, "cluster [NAME]", deleteCmd.Use)
}

func TestDeleteCluster_TooManyArgs(t *testing.T) {
	_, _, err := executeCommand("delete", "cluster", "a", "b")
	assert.Error(t, err)
}

func TestDeleteCluster_HasAllFlag(t *testing.T) {
	cmd := NewRootCommand()
	deleteCmd, _, err := cmd.Find([]string{"delete", "cluster"})
	require.NoError(t, err)
	f := deleteCmd.Flags().Lookup("all")
	require.NotNil(t, f)
	assert.Equal(t, "false", f.DefValue)
}

func TestDeleteCluster_AllRejectsArgs(t *testing.T) {
	_, _, err := executeCommand("delete", "cluster", "--all", "extra")
	assert.Error(t, err)
}
