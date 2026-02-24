// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- CreateClusterVolumes ---

func TestCreateClusterVolumes(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil) // config
	m.AddResult("", "", nil) // munge
	m.AddResult("", "", nil) // data
	c := docker.NewClient(&m)

	err := CreateClusterVolumes(context.Background(), c, "dev")

	require.NoError(t, err)
	require.Len(t, m.Calls, 3)
	assert.Equal(t, []string{"volume", "create", "sind-dev-config"}, m.Calls[0].Args)
	assert.Equal(t, []string{"volume", "create", "sind-dev-munge"}, m.Calls[1].Args)
	assert.Equal(t, []string{"volume", "create", "sind-dev-data"}, m.Calls[2].Args)
}

func TestCreateClusterVolumes_Error(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil)                                // config OK
	m.AddResult("", "", fmt.Errorf("volume create failed")) // munge fails
	c := docker.NewClient(&m)

	err := CreateClusterVolumes(context.Background(), c, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "munge")
	assert.Len(t, m.Calls, 2)
}
