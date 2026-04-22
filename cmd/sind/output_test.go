// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsJSONOutput(t *testing.T) {
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"get", "clusters", "--output", "json"})

	get, _, err := cmd.Find([]string{"get", "clusters"})
	require.NoError(t, err)

	// Before parsing, flag is not set.
	assert.False(t, isJSONOutput(get))
}

func TestIsJSONOutput_NoFlag(t *testing.T) {
	cmd := NewRootCommand()
	// version command has no --output flag
	ver, _, err := cmd.Find([]string{"version"})
	require.NoError(t, err)
	assert.False(t, isJSONOutput(ver))
}

func TestGetOutput_DefaultIsHuman(t *testing.T) {
	cmd := NewRootCommand()
	get, _, err := cmd.Find([]string{"get", "clusters"})
	require.NoError(t, err)
	assert.Equal(t, "human", outputFlag(get))
	assert.False(t, isJSONOutput(get))
}

func TestGetOutput_RejectsUnknownValue(t *testing.T) {
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"get", "clusters", "--output", "yaml"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid --output value "yaml"`)
}
