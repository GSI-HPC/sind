// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseNodeArgs_Simple(t *testing.T) {
	targets, err := parseNodeArgs("compute-0")
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, "compute-0", targets[0].ShortName)
	assert.Equal(t, "default", targets[0].Cluster)
}

func TestParseNodeArgs_WithCluster(t *testing.T) {
	targets, err := parseNodeArgs("compute-0.dev")
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, "compute-0", targets[0].ShortName)
	assert.Equal(t, "dev", targets[0].Cluster)
}

func TestParseNodeArgs_Nodeset(t *testing.T) {
	targets, err := parseNodeArgs("compute-[0-2].dev")
	require.NoError(t, err)
	require.Len(t, targets, 3)
	assert.Equal(t, "compute-0", targets[0].ShortName)
	assert.Equal(t, "dev", targets[0].Cluster)
	assert.Equal(t, "compute-2", targets[2].ShortName)
}

func TestParseNodeArgs_MultipleSpecs(t *testing.T) {
	targets, err := parseNodeArgs("controller.dev,compute-[0-1].prod")
	require.NoError(t, err)
	require.Len(t, targets, 3)

	groups := groupByCluster(targets)
	assert.Equal(t, []string{"controller"}, groups["dev"])
	assert.Equal(t, []string{"compute-0", "compute-1"}, groups["prod"])
}

func TestParseNodeArgs_EmptyShortName(t *testing.T) {
	_, err := parseNodeArgs(".dev")
	assert.Error(t, err)
}

func TestParseNodeArgs_TrailingDot(t *testing.T) {
	_, err := parseNodeArgs("compute-0.")
	assert.Error(t, err)
}

func TestGroupByCluster(t *testing.T) {
	targets := []nodeTarget{
		{ShortName: "compute-0", Cluster: "dev"},
		{ShortName: "compute-1", Cluster: "dev"},
		{ShortName: "compute-0", Cluster: "prod"},
	}

	groups := groupByCluster(targets)
	assert.Len(t, groups, 2)
	assert.Equal(t, []string{"compute-0", "compute-1"}, groups["dev"])
	assert.Equal(t, []string{"compute-0"}, groups["prod"])
}
