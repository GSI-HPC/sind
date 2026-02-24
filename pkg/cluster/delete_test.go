// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ListClusterResources ---

func TestListClusterResources(t *testing.T) {
	var m docker.MockExecutor
	// ListContainers: returns 2 containers
	m.AddResult(ndjson(
		psEntry{ID: "abc123", Names: "sind-dev-controller", State: "running", Image: "sind-node:latest"},
		psEntry{ID: "def456", Names: "sind-dev-compute-0", State: "running", Image: "sind-node:latest"},
	), "", nil)
	// NetworkExists: sind-dev-net exists
	m.AddResult("", "", nil)
	// VolumeExists: config, munge, data
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	res, err := ListClusterResources(context.Background(), c, "dev")

	require.NoError(t, err)

	// Containers
	require.Len(t, res.Containers, 2)
	assert.Equal(t, docker.ContainerName("sind-dev-controller"), res.Containers[0].Name)
	assert.Equal(t, docker.ContainerName("sind-dev-compute-0"), res.Containers[1].Name)

	// Network
	assert.Equal(t, docker.NetworkName("sind-dev-net"), res.Network)
	assert.True(t, res.NetworkExists)

	// Volumes
	assert.Equal(t, []docker.VolumeName{"sind-dev-config", "sind-dev-munge", "sind-dev-data"}, res.Volumes)
}

func TestListClusterResources_NoResources(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil) // ListContainers: empty
	addNotFound(t, &m, 1)    // NetworkExists: not found
	addNotFound(t, &m, 3)    // VolumeExists: config, munge, data
	c := docker.NewClient(&m)

	res, err := ListClusterResources(context.Background(), c, "nonexistent")

	require.NoError(t, err)
	assert.Empty(t, res.Containers)
	assert.False(t, res.NetworkExists)
	assert.Empty(t, res.Volumes)
}

func TestListClusterResources_PartialVolumes(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil) // ListContainers: empty
	m.AddResult("", "", nil) // NetworkExists: exists
	m.AddResult("", "", nil) // VolumeExists: config exists
	addNotFound(t, &m, 1)    // VolumeExists: munge missing
	m.AddResult("", "", nil) // VolumeExists: data exists
	c := docker.NewClient(&m)

	res, err := ListClusterResources(context.Background(), c, "dev")

	require.NoError(t, err)
	assert.True(t, res.NetworkExists)
	assert.Equal(t, []docker.VolumeName{"sind-dev-config", "sind-dev-data"}, res.Volumes)
}

func TestListClusterResources_ListContainersError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("docker ps failed"))
	c := docker.NewClient(&m)

	_, err := ListClusterResources(context.Background(), c, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing containers")
}

func TestListClusterResources_NetworkCheckError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil)                                  // ListContainers: empty
	m.AddResult("", "", fmt.Errorf("network inspect failed")) // non-exit error
	c := docker.NewClient(&m)

	_, err := ListClusterResources(context.Background(), c, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking network")
}

func TestListClusterResources_VolumeCheckError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil)                                 // ListContainers: empty
	m.AddResult("", "", nil)                                 // NetworkExists: exists
	m.AddResult("", "", fmt.Errorf("volume inspect failed")) // config check fails
	c := docker.NewClient(&m)

	_, err := ListClusterResources(context.Background(), c, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking volume")
}

func TestListClusterResources_LabelFilter(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil) // ListContainers: empty
	addNotFound(t, &m, 4)    // network + 3 volumes
	c := docker.NewClient(&m)

	_, err := ListClusterResources(context.Background(), c, "myCluster")

	require.NoError(t, err)
	require.Len(t, m.Calls, 5)
	// First call is docker ps with label filter
	args := m.Calls[0].Args
	assert.Contains(t, args, "--filter")
	filterIdx := indexOf(args, "--filter")
	require.Greater(t, filterIdx, -1)
	assert.Equal(t, "label=sind.cluster=myCluster", args[filterIdx+1])
}

// --- helpers ---

type psEntry struct {
	ID    string `json:"ID"`
	Names string `json:"Names"`
	State string `json:"State"`
	Image string `json:"Image"`
}

// ndjson builds newline-delimited JSON from the given entries.
func ndjson(entries ...psEntry) string {
	var lines []string
	for _, e := range entries {
		data, _ := json.Marshal(e)
		lines = append(lines, string(data))
	}
	return strings.Join(lines, "\n") + "\n"
}

// indexOf returns the index of s in slice, or -1 if not found.
func indexOf(slice []string, s string) int {
	for i, v := range slice {
		if v == s {
			return i
		}
	}
	return -1
}
