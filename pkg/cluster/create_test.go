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

// --- NodeRunConfigs ---

func TestNodeRunConfigs_Minimal(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: "controller", Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: "compute", Count: 1, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	configs := NodeRunConfigs(cfg, "172.18.0.2", "25.11.0")

	require.Len(t, configs, 2)
	assert.Equal(t, "controller", configs[0].ShortName)
	assert.Equal(t, "controller", configs[0].Role)
	assert.Equal(t, "compute-0", configs[1].ShortName)
	assert.Equal(t, "compute", configs[1].Role)
	assert.True(t, configs[1].Managed, "compute defaults to managed")
	// Shared fields
	for _, c := range configs {
		assert.Equal(t, "dev", c.ClusterName)
		assert.Equal(t, "img:1", c.Image)
		assert.Equal(t, "172.18.0.2", c.DNSIP)
		assert.Equal(t, "25.11.0", c.SlurmVersion)
	}
}

func TestNodeRunConfigs_MultiComputeGroups(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: "controller", Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: "compute", Count: 2, Image: "img:1", CPUs: 4, Memory: "8g", TmpSize: "1g"},
			{Role: "compute", Count: 1, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	configs := NodeRunConfigs(cfg, "", "")

	require.Len(t, configs, 4)
	assert.Equal(t, "compute-0", configs[1].ShortName)
	assert.Equal(t, 4, configs[1].CPUs)
	assert.Equal(t, "compute-1", configs[2].ShortName)
	assert.Equal(t, 4, configs[2].CPUs)
	assert.Equal(t, "compute-2", configs[3].ShortName)
	assert.Equal(t, 2, configs[3].CPUs)
}

func TestNodeRunConfigs_WithSubmitter(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: "controller", Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: "submitter", Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: "compute", Count: 1, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	configs := NodeRunConfigs(cfg, "", "")

	require.Len(t, configs, 3)
	assert.Equal(t, "controller", configs[0].ShortName)
	assert.Equal(t, "submitter", configs[1].ShortName)
	assert.Equal(t, "compute-0", configs[2].ShortName)
}

func TestNodeRunConfigs_ComputeDefaultCount(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: "controller", Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: "compute", Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	configs := NodeRunConfigs(cfg, "", "")

	require.Len(t, configs, 2)
	assert.Equal(t, "compute-0", configs[1].ShortName)
}

func TestNodeRunConfigs_UnmanagedCompute(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: "controller", Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: "compute", Count: 2, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g",
				Managed: boolPtr(false)},
			{Role: "compute", Count: 1, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	configs := NodeRunConfigs(cfg, "", "")

	require.Len(t, configs, 4)
	assert.False(t, configs[1].Managed, "compute-0 unmanaged")
	assert.False(t, configs[2].Managed, "compute-1 unmanaged")
	assert.True(t, configs[3].Managed, "compute-2 managed")
}

func TestNodeRunConfigs_HostPathStorage(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Storage: config.Storage{
			DataStorage: config.DataStorage{
				Type:      "hostPath",
				HostPath:  "/data/shared",
				MountPath: "/shared",
			},
		},
		Nodes: []config.Node{
			{Role: "controller", Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	configs := NodeRunConfigs(cfg, "", "")

	require.Len(t, configs, 1)
	assert.Equal(t, "/data/shared", configs[0].DataHostPath)
	assert.Equal(t, "/shared", configs[0].DataMountPath)
}

func TestNodeRunConfigs_VolumeStorage(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: "controller", Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	configs := NodeRunConfigs(cfg, "", "")

	require.Len(t, configs, 1)
	assert.Empty(t, configs[0].DataHostPath)
	assert.Empty(t, configs[0].DataMountPath)
}

// --- CreateClusterNodes ---

func TestCreateClusterNodes(t *testing.T) {
	var m docker.MockExecutor
	// Node 1: CreateContainer + ConnectNetwork + StartContainer
	m.AddResult("id1\n", "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	// Node 2: CreateContainer + ConnectNetwork + StartContainer
	m.AddResult("id2\n", "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	configs := []RunConfig{
		{ClusterName: "dev", ShortName: "controller", Role: "controller",
			Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		{ClusterName: "dev", ShortName: "compute-0", Role: "compute",
			Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
	}

	err := CreateClusterNodes(context.Background(), c, configs)

	require.NoError(t, err)
	assert.Len(t, m.Calls, 6) // 2 nodes × 3 calls each
}

func TestCreateClusterNodes_Error(t *testing.T) {
	var m docker.MockExecutor
	// Node 1: success
	m.AddResult("id1\n", "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	// Node 2: CreateContainer fails
	m.AddResult("", "", fmt.Errorf("image not found"))
	c := docker.NewClient(&m)

	configs := []RunConfig{
		{ClusterName: "dev", ShortName: "controller", Role: "controller",
			Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		{ClusterName: "dev", ShortName: "compute-0", Role: "compute",
			Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
	}

	err := CreateClusterNodes(context.Background(), c, configs)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "compute-0")
	assert.Len(t, m.Calls, 4) // 3 (node1) + 1 (node2 fails on create)
}

func TestCreateClusterNodes_Empty(t *testing.T) {
	var m docker.MockExecutor
	c := docker.NewClient(&m)

	err := CreateClusterNodes(context.Background(), c, nil)

	require.NoError(t, err)
	assert.Empty(t, m.Calls)
}

// --- EnableSlurmServices ---

func TestEnableSlurmServices(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil) // slurmctld on controller
	m.AddResult("", "", nil) // slurmd on compute-0
	c := docker.NewClient(&m)

	configs := []RunConfig{
		{ClusterName: "dev", ShortName: "controller", Role: "controller"},
		{ClusterName: "dev", ShortName: "compute-0", Role: "compute", Managed: true},
	}

	err := EnableSlurmServices(context.Background(), c, configs)

	require.NoError(t, err)
	require.Len(t, m.Calls, 2)
	assert.Equal(t, []string{"exec", "sind-dev-controller", "systemctl", "enable", "--now", "slurmctld"},
		m.Calls[0].Args)
	assert.Equal(t, []string{"exec", "sind-dev-compute-0", "systemctl", "enable", "--now", "slurmd"},
		m.Calls[1].Args)
}

func TestEnableSlurmServices_SkipsSubmitter(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil) // slurmctld on controller
	c := docker.NewClient(&m)

	configs := []RunConfig{
		{ClusterName: "dev", ShortName: "controller", Role: "controller"},
		{ClusterName: "dev", ShortName: "submitter", Role: "submitter"},
	}

	err := EnableSlurmServices(context.Background(), c, configs)

	require.NoError(t, err)
	assert.Len(t, m.Calls, 1) // only controller
}

func TestEnableSlurmServices_SkipsUnmanaged(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil) // slurmctld on controller
	c := docker.NewClient(&m)

	configs := []RunConfig{
		{ClusterName: "dev", ShortName: "controller", Role: "controller"},
		{ClusterName: "dev", ShortName: "compute-0", Role: "compute", Managed: false},
		{ClusterName: "dev", ShortName: "compute-1", Role: "compute", Managed: true},
	}

	// Need result for compute-1 slurmd
	m.AddResult("", "", nil)

	err := EnableSlurmServices(context.Background(), c, configs)

	require.NoError(t, err)
	require.Len(t, m.Calls, 2)
	// Controller + compute-1 only; compute-0 skipped
	assert.Contains(t, m.Calls[1].Args, "sind-dev-compute-1")
}

func TestEnableSlurmServices_Error(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("systemctl failed"))
	c := docker.NewClient(&m)

	configs := []RunConfig{
		{ClusterName: "dev", ShortName: "controller", Role: "controller"},
	}

	err := EnableSlurmServices(context.Background(), c, configs)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "enabling slurmctld on controller")
}
