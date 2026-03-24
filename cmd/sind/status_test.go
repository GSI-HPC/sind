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
	tests := []struct {
		name     string
		services map[string]bool
		want     string
	}{
		{"empty", nil, ""},
		{"single healthy", map[string]bool{"slurmctld": true}, "slurmctld \u2713"},
		{"single unhealthy", map[string]bool{"slurmd": false}, "slurmd \u2717"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatServices(tt.services)
			if len(tt.services) <= 1 {
				assert.Equal(t, tt.want, got)
			} else {
				// Multiple services: just check all parts are present
				for name, ok := range tt.services {
					assert.Contains(t, got, name+" "+checkmark(ok))
				}
			}
		})
	}
}
