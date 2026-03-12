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
