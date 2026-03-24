// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"testing"

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
