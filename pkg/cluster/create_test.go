// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- CreateClusterNetwork ---

func TestCreateClusterNetwork(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("net-id-123\n", "", nil)
	c := docker.NewClient(&m)

	err := CreateClusterNetwork(context.Background(), c, "dev")

	require.NoError(t, err)
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "create", "sind-dev-net"}, m.Calls[0].Args)
}

func TestCreateClusterNetwork_Error(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("network already exists"))
	c := docker.NewClient(&m)

	err := CreateClusterNetwork(context.Background(), c, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating cluster network")
}

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

// --- WriteClusterConfig ---

func TestWriteClusterConfig(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("abc123\n", "", nil) // CreateContainer (helper)
	m.AddResult("", "", nil)         // CopyToContainer
	m.AddResult("", "", nil)         // RemoveContainer (defer)
	c := docker.NewClient(&m)

	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: "controller"},
			{Role: "compute", Count: 2, CPUs: 2, Memory: "2g"},
		},
	}
	err := WriteClusterConfig(context.Background(), c, cfg)

	require.NoError(t, err)
	require.Len(t, m.Calls, 3)

	// CreateContainer mounts config volume
	assert.Equal(t, "create", m.Calls[0].Args[0])
	assert.Contains(t, m.Calls[0].Args, "sind-dev-config:/etc/slurm")
	assert.Contains(t, m.Calls[0].Args, "sind-dev-config-helper")

	// CopyToContainer writes to /etc/slurm
	assert.Equal(t, "cp", m.Calls[1].Args[0])
	assert.Contains(t, m.Calls[1].Args[len(m.Calls[1].Args)-1], "sind-dev-config-helper:/etc/slurm")

	// RemoveContainer cleans up
	assert.Equal(t, []string{"rm", "sind-dev-config-helper"}, m.Calls[2].Args)
}

func TestWriteClusterConfig_CreateError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("create failed"))
	c := docker.NewClient(&m)

	cfg := &config.Cluster{Name: "dev"}
	err := WriteClusterConfig(context.Background(), c, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating config helper")
}

func TestWriteClusterConfig_CopyError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("abc123\n", "", nil)            // CreateContainer
	m.AddResult("", "", fmt.Errorf("cp failed")) // CopyToContainer
	m.AddResult("", "", nil)                     // RemoveContainer (defer)
	c := docker.NewClient(&m)

	cfg := &config.Cluster{Name: "dev"}
	err := WriteClusterConfig(context.Background(), c, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "writing slurm config")
	assert.Len(t, m.Calls, 3) // defer still runs
}

// --- WriteMungeKey ---

func TestWriteMungeKey(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("abc123\n", "", nil) // CreateContainer (helper)
	m.AddResult("", "", nil)         // CopyToContainer
	m.AddResult("", "", nil)         // RemoveContainer (defer)
	c := docker.NewClient(&m)

	key := []byte("test-munge-key-data")
	err := WriteMungeKey(context.Background(), c, "dev", key)

	require.NoError(t, err)
	require.Len(t, m.Calls, 3)

	// CreateContainer mounts munge volume
	assert.Equal(t, "create", m.Calls[0].Args[0])
	assert.Contains(t, m.Calls[0].Args, "sind-dev-munge:/etc/munge")
	assert.Contains(t, m.Calls[0].Args, "sind-dev-munge-helper")

	// CopyToContainer writes to /etc/munge
	assert.Equal(t, "cp", m.Calls[1].Args[0])
	assert.Contains(t, m.Calls[1].Args[len(m.Calls[1].Args)-1], "sind-dev-munge-helper:/etc/munge")

	// RemoveContainer cleans up
	assert.Equal(t, []string{"rm", "sind-dev-munge-helper"}, m.Calls[2].Args)
}

func TestWriteMungeKey_CreateError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("create failed"))
	c := docker.NewClient(&m)

	err := WriteMungeKey(context.Background(), c, "dev", []byte("key"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating munge helper")
}

func TestWriteMungeKey_CopyError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("abc123\n", "", nil)            // CreateContainer
	m.AddResult("", "", fmt.Errorf("cp failed")) // CopyToContainer
	m.AddResult("", "", nil)                     // RemoveContainer (defer)
	c := docker.NewClient(&m)

	err := WriteMungeKey(context.Background(), c, "dev", []byte("key"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "writing munge key")
	assert.Len(t, m.Calls, 3) // defer still runs
}
