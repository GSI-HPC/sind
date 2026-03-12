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

func TestGetClusters(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult(ndjson(
		psEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=dev,sind.role=controller,sind.slurm.version=25.11.0",
		},
		psEntry{
			ID: "b", Names: "sind-dev-compute-0", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=dev,sind.role=compute,sind.slurm.version=25.11.0",
		},
		psEntry{
			ID: "c", Names: "sind-dev-compute-1", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=dev,sind.role=compute,sind.slurm.version=25.11.0",
		},
		psEntry{
			ID: "d", Names: "sind-prod-controller", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=prod,sind.role=controller,sind.slurm.version=25.11.0",
		},
		psEntry{
			ID: "e", Names: "sind-prod-submitter", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=prod,sind.role=submitter,sind.slurm.version=25.11.0",
		},
		psEntry{
			ID: "f", Names: "sind-prod-compute-0", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=prod,sind.role=compute,sind.slurm.version=25.11.0",
		},
		psEntry{
			ID: "g", Names: "sind-prod-compute-1", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=prod,sind.role=compute,sind.slurm.version=25.11.0",
		},
	), "", nil)
	c := docker.NewClient(&m)

	clusters, err := GetClusters(context.Background(), c)

	require.NoError(t, err)
	require.Len(t, clusters, 2)

	// Results should be sorted by name.
	assert.Equal(t, "dev", clusters[0].Name)
	assert.Equal(t, "25.11.0", clusters[0].SlurmVersion)
	assert.Equal(t, StatusRunning, clusters[0].Status)
	assert.Equal(t, 3, clusters[0].NodeCount)
	assert.Equal(t, 0, clusters[0].Submitters)
	assert.Equal(t, 1, clusters[0].Controllers)
	assert.Equal(t, 2, clusters[0].Workers)

	assert.Equal(t, "prod", clusters[1].Name)
	assert.Equal(t, "25.11.0", clusters[1].SlurmVersion)
	assert.Equal(t, StatusRunning, clusters[1].Status)
	assert.Equal(t, 4, clusters[1].NodeCount)
	assert.Equal(t, 1, clusters[1].Submitters)
	assert.Equal(t, 1, clusters[1].Controllers)
	assert.Equal(t, 2, clusters[1].Workers)
}

func TestGetClusters_Empty(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	clusters, err := GetClusters(context.Background(), c)

	require.NoError(t, err)
	assert.Empty(t, clusters)
}

func TestGetClusters_MixedStatus(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult(ndjson(
		psEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=controller",
		},
		psEntry{
			ID: "b", Names: "sind-dev-compute-0", State: "exited", Image: "img",
			Labels: "sind.cluster=dev,sind.role=compute",
		},
	), "", nil)
	c := docker.NewClient(&m)

	clusters, err := GetClusters(context.Background(), c)

	require.NoError(t, err)
	require.Len(t, clusters, 1)
	// Mixed states result in unknown status.
	assert.Equal(t, StatusUnknown, clusters[0].Status)
}

func TestGetClusters_AllStopped(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult(ndjson(
		psEntry{
			ID: "a", Names: "sind-dev-controller", State: "exited", Image: "img",
			Labels: "sind.cluster=dev,sind.role=controller",
		},
		psEntry{
			ID: "b", Names: "sind-dev-compute-0", State: "exited", Image: "img",
			Labels: "sind.cluster=dev,sind.role=compute",
		},
	), "", nil)
	c := docker.NewClient(&m)

	clusters, err := GetClusters(context.Background(), c)

	require.NoError(t, err)
	require.Len(t, clusters, 1)
	assert.Equal(t, StatusStopped, clusters[0].Status)
}

func TestGetClusters_Error(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("docker daemon not running"))
	c := docker.NewClient(&m)

	_, err := GetClusters(context.Background(), c)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing sind containers")
}

func TestGetClusters_LabelFilter(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	_, _ = GetClusters(context.Background(), c)

	require.Len(t, m.Calls, 1)
	args := m.Calls[0].Args
	filterIdx := indexOf(args, "--filter")
	require.Greater(t, filterIdx, -1)
	assert.Equal(t, "label=sind.cluster", args[filterIdx+1])
}

func TestGetClusters_NoSlurmVersion(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult(ndjson(
		psEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=controller",
		},
	), "", nil)
	c := docker.NewClient(&m)

	clusters, err := GetClusters(context.Background(), c)

	require.NoError(t, err)
	require.Len(t, clusters, 1)
	assert.Equal(t, "", clusters[0].SlurmVersion)
}

// --- GetNodes ---

func TestGetNodes(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult(ndjson(
		psEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=controller",
		},
		psEntry{
			ID: "b", Names: "sind-dev-compute-1", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=compute",
		},
		psEntry{
			ID: "c", Names: "sind-dev-compute-0", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=compute",
		},
	), "", nil)
	c := docker.NewClient(&m)

	nodes, err := GetNodes(context.Background(), c, "dev")

	require.NoError(t, err)
	require.Len(t, nodes, 3)

	// Sorted: controller first, then compute by name.
	assert.Equal(t, "controller", nodes[0].Name)
	assert.Equal(t, "controller", nodes[0].Role)
	assert.Equal(t, StatusRunning, nodes[0].Status)

	assert.Equal(t, "compute-0", nodes[1].Name)
	assert.Equal(t, "compute", nodes[1].Role)

	assert.Equal(t, "compute-1", nodes[2].Name)
	assert.Equal(t, "compute", nodes[2].Role)
}

func TestGetNodes_WithStatus(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult(ndjson(
		psEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=controller",
		},
		psEntry{
			ID: "b", Names: "sind-dev-compute-0", State: "exited", Image: "img",
			Labels: "sind.cluster=dev,sind.role=compute",
		},
		psEntry{
			ID: "c", Names: "sind-dev-compute-1", State: "paused", Image: "img",
			Labels: "sind.cluster=dev,sind.role=compute",
		},
	), "", nil)
	c := docker.NewClient(&m)

	nodes, err := GetNodes(context.Background(), c, "dev")

	require.NoError(t, err)
	require.Len(t, nodes, 3)
	assert.Equal(t, StatusRunning, nodes[0].Status)
	assert.Equal(t, StatusStopped, nodes[1].Status)
	assert.Equal(t, StatusPaused, nodes[2].Status)
}

func TestGetNodes_Empty(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	nodes, err := GetNodes(context.Background(), c, "nonexistent")

	require.NoError(t, err)
	assert.Empty(t, nodes)
}

func TestGetNodes_Error(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("docker ps failed"))
	c := docker.NewClient(&m)

	_, err := GetNodes(context.Background(), c, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing containers")
}

func TestGetNodes_LabelFilter(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	_, _ = GetNodes(context.Background(), c, "myCluster")

	require.Len(t, m.Calls, 1)
	args := m.Calls[0].Args
	filterIdx := indexOf(args, "--filter")
	require.Greater(t, filterIdx, -1)
	assert.Equal(t, "label=sind.cluster=myCluster", args[filterIdx+1])
}

func TestGetNodes_SortOrder(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult(ndjson(
		psEntry{
			ID: "a", Names: "sind-dev-compute-0", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=compute",
		},
		psEntry{
			ID: "b", Names: "sind-dev-submitter", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=submitter",
		},
		psEntry{
			ID: "c", Names: "sind-dev-controller", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=controller",
		},
	), "", nil)
	c := docker.NewClient(&m)

	nodes, err := GetNodes(context.Background(), c, "dev")

	require.NoError(t, err)
	require.Len(t, nodes, 3)
	// Order: controller, submitter, compute
	assert.Equal(t, "controller", nodes[0].Role)
	assert.Equal(t, "submitter", nodes[1].Role)
	assert.Equal(t, "compute", nodes[2].Role)
}

// --- GetNetworks ---

type networkEntry struct {
	Name   string `json:"Name"`
	Driver string `json:"Driver"`
}

func ndjsonNetworks(entries ...networkEntry) string {
	var lines []string
	for _, e := range entries {
		data, _ := json.Marshal(e)
		lines = append(lines, string(data))
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestGetNetworks(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult(ndjsonNetworks(
		networkEntry{Name: "sind-dev-net", Driver: "bridge"},
		networkEntry{Name: "sind-mesh", Driver: "bridge"},
		networkEntry{Name: "sind-prod-net", Driver: "bridge"},
	), "", nil)
	c := docker.NewClient(&m)

	networks, err := GetNetworks(context.Background(), c)

	require.NoError(t, err)
	require.Len(t, networks, 3)
	// Sorted by name.
	assert.Equal(t, "sind-dev-net", networks[0].Name)
	assert.Equal(t, "bridge", networks[0].Driver)
	assert.Equal(t, "sind-mesh", networks[1].Name)
	assert.Equal(t, "sind-prod-net", networks[2].Name)
}

func TestGetNetworks_Empty(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	networks, err := GetNetworks(context.Background(), c)

	require.NoError(t, err)
	assert.Empty(t, networks)
}

func TestGetNetworks_Error(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("docker daemon not running"))
	c := docker.NewClient(&m)

	_, err := GetNetworks(context.Background(), c)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing networks")
}

// --- GetVolumes ---

type volumeEntry struct {
	Name   string `json:"Name"`
	Driver string `json:"Driver"`
}

func ndjsonVolumes(entries ...volumeEntry) string {
	var lines []string
	for _, e := range entries {
		data, _ := json.Marshal(e)
		lines = append(lines, string(data))
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestGetVolumes(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult(ndjsonVolumes(
		volumeEntry{Name: "sind-dev-data", Driver: "local"},
		volumeEntry{Name: "sind-dev-config", Driver: "local"},
		volumeEntry{Name: "sind-dev-munge", Driver: "local"},
		volumeEntry{Name: "sind-ssh-config", Driver: "local"},
	), "", nil)
	c := docker.NewClient(&m)

	volumes, err := GetVolumes(context.Background(), c)

	require.NoError(t, err)
	require.Len(t, volumes, 4)
	// Sorted by name.
	assert.Equal(t, "sind-dev-config", volumes[0].Name)
	assert.Equal(t, "local", volumes[0].Driver)
	assert.Equal(t, "sind-dev-data", volumes[1].Name)
	assert.Equal(t, "sind-dev-munge", volumes[2].Name)
	assert.Equal(t, "sind-ssh-config", volumes[3].Name)
}

func TestGetVolumes_Empty(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	volumes, err := GetVolumes(context.Background(), c)

	require.NoError(t, err)
	assert.Empty(t, volumes)
}

func TestGetVolumes_Error(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("docker daemon not running"))
	c := docker.NewClient(&m)

	_, err := GetVolumes(context.Background(), c)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing volumes")
}
