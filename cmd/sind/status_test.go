// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatus_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"status"})
	require.NoError(t, err)
	assert.Equal(t, "status [CLUSTER]", c.Use)
}

func TestStatus_TooManyArgs(t *testing.T) {
	_, _, err := executeCommand("status", "a", "b")
	assert.Error(t, err)
}

func TestCheckmark(t *testing.T) {
	assert.Equal(t, "\u2713", checkmark(true))
	assert.Equal(t, "\u2717", checkmark(false))
}

func TestFormatServices(t *testing.T) {
	assert.Equal(t, "", formatServices(nil))
	assert.Equal(t, "slurmctld \u2713", formatServices(map[string]bool{"slurmctld": true}))
	assert.Equal(t, "slurmd \u2717", formatServices(map[string]bool{"slurmd": false}))
}

func TestFormatServices_Multiple(t *testing.T) {
	services := map[string]bool{"slurmctld": true, "slurmd": false}
	got := formatServices(services)
	assert.Contains(t, got, "slurmctld \u2713")
	assert.Contains(t, got, "slurmd \u2717")
}
