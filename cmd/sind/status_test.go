// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"testing"

	"github.com/GSI-HPC/sind/pkg/cluster"
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

func TestFormatState(t *testing.T) {
	tests := []struct {
		name  string
		nodes []*cluster.NodeStatus
		state cluster.State
		want  string
	}{
		{
			"all running",
			[]*cluster.NodeStatus{
				{Health: &cluster.NodeHealth{Container: "running"}},
				{Health: &cluster.NodeHealth{Container: "running"}},
			},
			cluster.StateRunning,
			"running (2/0/0/2)",
		},
		{
			"mixed",
			[]*cluster.NodeStatus{
				{Health: &cluster.NodeHealth{Container: "running"}},
				{Health: &cluster.NodeHealth{Container: "exited"}},
			},
			cluster.StateMixed,
			"mixed (1/1/0/2)",
		},
		{
			"empty",
			nil,
			cluster.StateEmpty,
			"empty (0/0/0/0)",
		},
		{
			"paused",
			[]*cluster.NodeStatus{
				{Health: &cluster.NodeHealth{Container: "paused"}},
			},
			cluster.StatePaused,
			"paused (0/0/1/1)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &cluster.Status{State: tt.state, Nodes: tt.nodes}
			assert.Equal(t, tt.want, formatState(s))
		})
	}
}

func TestFormatServices(t *testing.T) {
	assert.Equal(t, "", formatServices(nil))
	assert.Equal(t, "slurmctld \u2713", formatServices(map[string]bool{"slurmctld": true}))
	assert.Equal(t, "slurmd \u2717", formatServices(map[string]bool{"slurmd": false}))
}

func TestFormatServices_Multiple(t *testing.T) {
	services := map[string]bool{"slurmctld": true, "slurmd": false}
	got := formatServices(services)
	assert.Equal(t, "slurmctld \u2713 slurmd \u2717", got)
}
