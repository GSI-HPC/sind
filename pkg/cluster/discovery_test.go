// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ListClusterResources ---

func TestListClusterResources(t *testing.T) {
	var m cmdexec.MockExecutor
	// ListContainers: returns 2 containers
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{ID: "abc123", Names: "sind-dev-controller", State: "running", Image: "sind-node:latest"},
		testutil.PsEntry{ID: "def456", Names: "sind-dev-worker-0", State: "running", Image: "sind-node:latest"},
	), "", nil)
	// NetworkExists: sind-dev-net exists
	m.AddResult("", "", nil)
	// VolumeExists: config, munge, data
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	res, err := ListClusterResources(t.Context(), c, mesh.DefaultRealm, "dev")

	require.NoError(t, err)

	// Containers
	require.Len(t, res.Containers, 2)
	assert.Equal(t, docker.ContainerName("sind-dev-controller"), res.Containers[0].Name)
	assert.Equal(t, docker.ContainerName("sind-dev-worker-0"), res.Containers[1].Name)

	// Network
	assert.Equal(t, docker.NetworkName("sind-dev-net"), res.Network)
	assert.True(t, res.NetworkExists)

	// Volumes
	assert.Equal(t, []docker.VolumeName{"sind-dev-config", "sind-dev-munge", "sind-dev-data"}, res.Volumes)
}

func TestListClusterResources_NoResources(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil) // ListContainers: empty
	addNotFound(t, &m, 1)    // NetworkExists: not found
	addNotFound(t, &m, 3)    // VolumeExists: config, munge, data
	c := docker.NewClient(&m)

	res, err := ListClusterResources(t.Context(), c, mesh.DefaultRealm, "nonexistent")

	require.NoError(t, err)
	assert.Empty(t, res.Containers)
	assert.False(t, res.NetworkExists)
	assert.Empty(t, res.Volumes)
}

func TestListClusterResources_PartialVolumes(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil) // ListContainers: empty
	m.AddResult("", "", nil) // NetworkExists: exists
	m.AddResult("", "", nil) // VolumeExists: config exists
	addNotFound(t, &m, 1)    // VolumeExists: munge missing
	m.AddResult("", "", nil) // VolumeExists: data exists
	c := docker.NewClient(&m)

	res, err := ListClusterResources(t.Context(), c, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.True(t, res.NetworkExists)
	assert.Equal(t, []docker.VolumeName{"sind-dev-config", "sind-dev-data"}, res.Volumes)
}

func TestListClusterResources_ListContainersError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", fmt.Errorf("docker ps failed"))
	c := docker.NewClient(&m)

	_, err := ListClusterResources(t.Context(), c, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing containers")
}

func TestListClusterResources_NetworkCheckError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil)                                  // ListContainers: empty
	m.AddResult("", "", fmt.Errorf("network inspect failed")) // non-exit error
	c := docker.NewClient(&m)

	_, err := ListClusterResources(t.Context(), c, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking network")
}

func TestListClusterResources_VolumeCheckError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil)                                 // ListContainers: empty
	m.AddResult("", "", nil)                                 // NetworkExists: exists
	m.AddResult("", "", fmt.Errorf("volume inspect failed")) // config check fails
	c := docker.NewClient(&m)

	_, err := ListClusterResources(t.Context(), c, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking volume")
}

func TestListClusterResources_LabelFilter(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil) // ListContainers: empty
	addNotFound(t, &m, 4)    // network + 3 volumes
	c := docker.NewClient(&m)

	_, err := ListClusterResources(t.Context(), c, mesh.DefaultRealm, "myCluster")

	require.NoError(t, err)
	require.Len(t, m.Calls, 5)
	// First call is docker ps with label filter
	args := m.Calls[0].Args
	assert.Contains(t, args, "--filter")
	filterIdx := indexOf(args, "--filter")
	require.Greater(t, filterIdx, -1)
	assert.Equal(t, "label=sind.cluster=myCluster", args[filterIdx+1])
}

// --- HasOtherClusters ---

func TestHasOtherClusters_True(t *testing.T) {
	var m cmdexec.MockExecutor
	// ListContainers returns containers from two clusters
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{ID: "a", Names: "sind-dev-controller", State: "running", Image: "img"},
		testutil.PsEntry{ID: "b", Names: "sind-prod-controller", State: "running", Image: "img"},
	), "", nil)
	c := docker.NewClient(&m)

	has, err := HasOtherClusters(t.Context(), c, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.True(t, has)
}

func TestHasOtherClusters_False(t *testing.T) {
	var m cmdexec.MockExecutor
	// Only containers from the same cluster
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{ID: "a", Names: "sind-dev-controller", State: "running", Image: "img"},
		testutil.PsEntry{ID: "b", Names: "sind-dev-worker-0", State: "running", Image: "img"},
	), "", nil)
	c := docker.NewClient(&m)

	has, err := HasOtherClusters(t.Context(), c, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.False(t, has)
}

func TestHasOtherClusters_NoContainers(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil) // empty list
	c := docker.NewClient(&m)

	has, err := HasOtherClusters(t.Context(), c, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.False(t, has)
}

func TestHasOtherClusters_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", fmt.Errorf("docker daemon not running"))
	c := docker.NewClient(&m)

	_, err := HasOtherClusters(t.Context(), c, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing containers")
}

func TestHasOtherClusters_LabelFilter(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	_, _ = HasOtherClusters(t.Context(), c, mesh.DefaultRealm, "dev")

	require.Len(t, m.Calls, 1)
	args := m.Calls[0].Args
	filterIdx := indexOf(args, "--filter")
	require.Greater(t, filterIdx, -1)
	assert.Equal(t, "label="+LabelRealm+"="+mesh.DefaultRealm, args[filterIdx+1])
}

func TestHasOtherClusters_PrefixAmbiguity(t *testing.T) {
	// Cluster "dev" must not match container "sind-dev2-controller".
	// The prefix includes the trailing dash: "sind-dev-".
	var m cmdexec.MockExecutor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{ID: "a", Names: "sind-dev-controller", State: "running", Image: "img"},
		testutil.PsEntry{ID: "b", Names: "sind-dev2-controller", State: "running", Image: "img"},
	), "", nil)
	c := docker.NewClient(&m)

	has, err := HasOtherClusters(t.Context(), c, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.True(t, has, "sind-dev2-controller should not match cluster dev")
}

// --- DiscoverClusterNames ---

func TestDiscoverClusterNames_FromNetworks(t *testing.T) {
	var m cmdexec.MockExecutor
	// ListNetworks: returns cluster network + mesh network
	m.AddResult(`{"Name":"sind-default-net","Driver":"bridge","ID":"a","Scope":"local"}
{"Name":"sind-mesh","Driver":"bridge","ID":"b","Scope":"local"}`, "", nil)
	// ListVolumes: empty
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	names, err := DiscoverClusterNames(t.Context(), c, mesh.DefaultRealm)

	require.NoError(t, err)
	assert.Equal(t, []string{"default"}, names)
}

func TestDiscoverClusterNames_FromVolumes(t *testing.T) {
	var m cmdexec.MockExecutor
	// ListNetworks: empty
	m.AddResult("", "", nil)
	// ListVolumes: orphaned volumes
	m.AddResult(`{"Name":"sind-default-config","Driver":"local","Mountpoint":"/data","Scope":"local"}
{"Name":"sind-default-munge","Driver":"local","Mountpoint":"/data","Scope":"local"}
{"Name":"sind-ssh-config","Driver":"local","Mountpoint":"/data","Scope":"local"}`, "", nil)
	c := docker.NewClient(&m)

	names, err := DiscoverClusterNames(t.Context(), c, mesh.DefaultRealm)

	require.NoError(t, err)
	assert.Equal(t, []string{"default"}, names)
}

func TestDiscoverClusterNames_MultipleClusters(t *testing.T) {
	var m cmdexec.MockExecutor
	// ListNetworks: two cluster networks
	m.AddResult(`{"Name":"sind-dev-net","Driver":"bridge","ID":"a","Scope":"local"}
{"Name":"sind-prod-net","Driver":"bridge","ID":"b","Scope":"local"}
{"Name":"sind-mesh","Driver":"bridge","ID":"c","Scope":"local"}`, "", nil)
	// ListVolumes: volumes for dev only
	m.AddResult(`{"Name":"sind-dev-config","Driver":"local","Mountpoint":"/data","Scope":"local"}`, "", nil)
	c := docker.NewClient(&m)

	names, err := DiscoverClusterNames(t.Context(), c, mesh.DefaultRealm)

	require.NoError(t, err)
	assert.Equal(t, []string{"dev", "prod"}, names)
}

func TestDiscoverClusterNames_Empty(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil) // ListNetworks
	m.AddResult("", "", nil) // ListVolumes
	c := docker.NewClient(&m)

	names, err := DiscoverClusterNames(t.Context(), c, mesh.DefaultRealm)

	require.NoError(t, err)
	assert.Empty(t, names)
}

func TestDiscoverClusterNames_NetworkError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", fmt.Errorf("docker daemon unreachable"))
	c := docker.NewClient(&m)

	_, err := DiscoverClusterNames(t.Context(), c, mesh.DefaultRealm)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing networks")
}

func TestDiscoverClusterNames_VolumeError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil) // ListNetworks: ok
	m.AddResult("", "", fmt.Errorf("docker daemon unreachable"))
	c := docker.NewClient(&m)

	_, err := DiscoverClusterNames(t.Context(), c, mesh.DefaultRealm)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing volumes")
}
