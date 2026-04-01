// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Resource Lifecycle ---

func TestClusterResourceLifecycle(t *testing.T) {
	c, rec := newTestClient(t)
	ctx := t.Context()
	clusterName := "it-res"

	if !rec.IsIntegration() {
		// CreateClusterNetwork
		rec.AddResult("net-id\n", "", nil)
		// CreateClusterVolumes (config, munge, data)
		rec.AddResult("", "", nil)
		rec.AddResult("", "", nil)
		rec.AddResult("", "", nil)
		// WriteClusterConfig (create helper, copy, remove helper)
		rec.AddResult("helper-id\n", "", nil)
		rec.AddResult("", "", nil)
		rec.AddResult("", "", nil)
		// WriteMungeKey (run helper, copy, chown, chmod, kill+remove helper)
		rec.AddResult("helper-id\n", "", nil)
		rec.AddResult("", "", nil)
		rec.AddResult("", "", nil)
		rec.AddResult("", "", nil)
		rec.AddResult("", "", nil)
		rec.AddResult("", "", nil)
		// Verify: network exists, volumes exist
		rec.AddResult("[{}]\n", "", nil)
		rec.AddResult("[{}]\n", "", nil)
		rec.AddResult("[{}]\n", "", nil)
		rec.AddResult("[{}]\n", "", nil)
		// Cleanup: remove volumes, network
		rec.AddResult("", "", nil)
		rec.AddResult("", "", nil)
		rec.AddResult("", "", nil)
		rec.AddResult("", "", nil)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_ = c.RemoveVolume(bg, VolumeName(mesh.DefaultRealm, clusterName, "config"))
		_ = c.RemoveVolume(bg, VolumeName(mesh.DefaultRealm, clusterName, "munge"))
		_ = c.RemoveVolume(bg, VolumeName(mesh.DefaultRealm, clusterName, "data"))
		_ = c.RemoveNetwork(bg, NetworkName(mesh.DefaultRealm, clusterName))
	})

	// Create network.
	err := CreateClusterNetwork(ctx, c, mesh.DefaultRealm, clusterName)
	require.NoError(t, err)

	// Create volumes.
	err = CreateClusterVolumes(ctx, c, mesh.DefaultRealm, clusterName, true)
	require.NoError(t, err)

	// Write config.
	helperImage := "busybox:latest"
	if rec.IsIntegration() {
		helperImage = "ghcr.io/gsi-hpc/sind-node:latest"
	}
	cfg := &config.Cluster{
		Name: clusterName,
		Nodes: []config.Node{
			{Role: "controller", CPUs: 2, Memory: "2g", Image: helperImage},
			{Role: "worker", Count: 1, CPUs: 2, Memory: "2g", Image: helperImage},
		},
	}
	err = WriteClusterConfig(ctx, c, mesh.DefaultRealm, cfg, helperImage, false)
	require.NoError(t, err)

	// Write munge key.
	err = WriteMungeKey(ctx, c, mesh.DefaultRealm, clusterName, []byte("test-munge-key-data"), helperImage, false)
	require.NoError(t, err)

	// Verify resources exist.
	exists, err := c.NetworkExists(ctx, NetworkName(mesh.DefaultRealm, clusterName))
	require.NoError(t, err)
	assert.True(t, exists, "cluster network")

	for _, vtype := range []string{"config", "munge", "data"} {
		exists, err = c.VolumeExists(ctx, VolumeName(mesh.DefaultRealm, clusterName, vtype))
		require.NoError(t, err)
		assert.True(t, exists, vtype+" volume")
	}

	t.Logf("docker I/O:\n%s", rec.Dump())
}

// --- CreateClusterNetwork ---

func TestCreateClusterNetwork(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("net-id-123\n", "", nil)
	c := docker.NewClient(&m)

	err := CreateClusterNetwork(t.Context(), c, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{
		"network", "create",
		"--label", "com.docker.compose.network=net",
		"--label", "com.docker.compose.project=sind-dev",
		"sind-dev-net",
	}, m.Calls[0].Args)
}

func TestCreateClusterNetwork_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", fmt.Errorf("network already exists"))
	c := docker.NewClient(&m)

	err := CreateClusterNetwork(t.Context(), c, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating cluster network")
}

// --- CreateClusterVolumes ---

func TestCreateClusterVolumes(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil) // config
	m.AddResult("", "", nil) // munge
	m.AddResult("", "", nil) // data
	c := docker.NewClient(&m)

	err := CreateClusterVolumes(t.Context(), c, mesh.DefaultRealm, "dev", true)

	require.NoError(t, err)
	require.Len(t, m.Calls, 3)
	assert.Equal(t, []string{
		"volume", "create",
		"--label", "com.docker.compose.project=sind-dev",
		"--label", "com.docker.compose.volume=config",
		"sind-dev-config",
	}, m.Calls[0].Args)
	assert.Equal(t, []string{
		"volume", "create",
		"--label", "com.docker.compose.project=sind-dev",
		"--label", "com.docker.compose.volume=munge",
		"sind-dev-munge",
	}, m.Calls[1].Args)
	assert.Equal(t, []string{
		"volume", "create",
		"--label", "com.docker.compose.project=sind-dev",
		"--label", "com.docker.compose.volume=data",
		"sind-dev-data",
	}, m.Calls[2].Args)
}

func TestCreateClusterVolumes_SkipData(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil) // config
	m.AddResult("", "", nil) // munge
	c := docker.NewClient(&m)

	err := CreateClusterVolumes(t.Context(), c, mesh.DefaultRealm, "dev", false)

	require.NoError(t, err)
	require.Len(t, m.Calls, 2)
	assert.Contains(t, m.Calls[0].Args[len(m.Calls[0].Args)-1], "config")
	assert.Contains(t, m.Calls[1].Args[len(m.Calls[1].Args)-1], "munge")
}

func TestCreateClusterVolumes_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil)                                // config OK
	m.AddResult("", "", fmt.Errorf("volume create failed")) // munge fails
	c := docker.NewClient(&m)

	err := CreateClusterVolumes(t.Context(), c, mesh.DefaultRealm, "dev", true)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "munge")
	assert.Len(t, m.Calls, 2)
}

// --- WriteClusterConfig ---

func TestWriteClusterConfig(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("abc123\n", "", nil) // CreateContainer (helper)
	m.AddResult("", "", nil)         // CopyToContainer
	m.AddResult("", "", nil)         // RemoveContainer (defer)
	c := docker.NewClient(&m)

	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: "controller"},
			{Role: "worker", Count: 2, CPUs: 2, Memory: "2g"},
		},
	}
	err := WriteClusterConfig(t.Context(), c, mesh.DefaultRealm, cfg, "busybox:latest", false)

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

func TestWriteClusterConfig_Pull(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("abc123\n", "", nil) // CreateContainer (helper)
	m.AddResult("", "", nil)         // CopyToContainer
	m.AddResult("", "", nil)         // RemoveContainer (defer)
	c := docker.NewClient(&m)

	cfg := &config.Cluster{Name: "dev"}
	err := WriteClusterConfig(t.Context(), c, mesh.DefaultRealm, cfg, "busybox:latest", true)

	require.NoError(t, err)
	createArgs := m.Calls[0].Args
	pull, ok := argValue(createArgs, "--pull")
	assert.True(t, ok, "--pull flag present")
	assert.Equal(t, "always", pull)
}

func TestWriteClusterConfig_CreateError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", fmt.Errorf("create failed"))
	c := docker.NewClient(&m)

	cfg := &config.Cluster{Name: "dev"}
	err := WriteClusterConfig(t.Context(), c, mesh.DefaultRealm, cfg, "busybox:latest", false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating config helper")
}

func TestWriteClusterConfig_CopyError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("abc123\n", "", nil)             // CreateContainer
	m.AddResult("", "", fmt.Errorf("cp failed")) // CopyToContainer
	m.AddResult("", "", nil)                     // RemoveContainer (defer)
	c := docker.NewClient(&m)

	cfg := &config.Cluster{Name: "dev"}
	err := WriteClusterConfig(t.Context(), c, mesh.DefaultRealm, cfg, "busybox:latest", false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "writing slurm config")
	assert.Len(t, m.Calls, 3) // defer still runs
}

// --- WriteMungeKey ---

func TestWriteMungeKey(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("abc123\n", "", nil) // RunContainer (helper)
	m.AddResult("", "", nil)         // CopyToContainer
	m.AddResult("", "", nil)         // Exec chown
	m.AddResult("", "", nil)         // Exec chmod
	m.AddResult("", "", nil)         // KillContainer (defer)
	m.AddResult("", "", nil)         // RemoveContainer (defer)
	c := docker.NewClient(&m)

	key := []byte("test-munge-key-data")
	err := WriteMungeKey(t.Context(), c, mesh.DefaultRealm, "dev", key, "busybox:latest", false)

	require.NoError(t, err)

	// RunContainer mounts munge volume
	assert.Equal(t, "run", m.Calls[0].Args[0])
	assert.Contains(t, m.Calls[0].Args, "sind-dev-munge:/etc/munge")

	// CopyToContainer + chown + chmod
	assert.Equal(t, "cp", m.Calls[1].Args[0])
	assert.Equal(t, []string{"exec", "sind-dev-munge-helper", "chown", "munge:munge", "/etc/munge/munge.key"}, m.Calls[2].Args)
	assert.Equal(t, []string{"exec", "sind-dev-munge-helper", "chmod", "0400", "/etc/munge/munge.key"}, m.Calls[3].Args)
}

func TestWriteMungeKey_Pull(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("abc123\n", "", nil) // RunContainer (helper)
	m.AddResult("", "", nil)         // CopyToContainer
	m.AddResult("", "", nil)         // Exec chown
	m.AddResult("", "", nil)         // Exec chmod
	m.AddResult("", "", nil)         // KillContainer (defer)
	m.AddResult("", "", nil)         // RemoveContainer (defer)
	c := docker.NewClient(&m)

	err := WriteMungeKey(t.Context(), c, mesh.DefaultRealm, "dev", []byte("key"), "busybox:latest", true)

	require.NoError(t, err)
	runArgs := m.Calls[0].Args
	pull, ok := argValue(runArgs, "--pull")
	assert.True(t, ok, "--pull flag present")
	assert.Equal(t, "always", pull)
}

func TestWriteMungeKey_RunError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", fmt.Errorf("run failed"))
	c := docker.NewClient(&m)

	err := WriteMungeKey(t.Context(), c, mesh.DefaultRealm, "dev", []byte("key"), "busybox:latest", false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating munge helper")
}

func TestWriteMungeKey_CopyError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("abc123\n", "", nil)             // RunContainer
	m.AddResult("", "", fmt.Errorf("cp failed")) // CopyToContainer
	m.AddResult("", "", nil)                     // KillContainer (defer)
	m.AddResult("", "", nil)                     // RemoveContainer (defer)
	c := docker.NewClient(&m)

	err := WriteMungeKey(t.Context(), c, mesh.DefaultRealm, "dev", []byte("key"), "busybox:latest", false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "writing munge key")
	assert.Len(t, m.Calls, 4) // defer runs kill+rm
}

func TestWriteMungeKey_ChownError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("abc123\n", "", nil)                // RunContainer
	m.AddResult("", "", nil)                        // CopyToContainer
	m.AddResult("", "", fmt.Errorf("chown failed")) // Exec chown
	m.AddResult("", "", nil)                        // KillContainer (defer)
	m.AddResult("", "", nil)                        // RemoveContainer (defer)
	c := docker.NewClient(&m)

	err := WriteMungeKey(t.Context(), c, mesh.DefaultRealm, "dev", []byte("key"), "busybox:latest", false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "fixing munge key ownership")
}

func TestWriteMungeKey_ChmodError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("abc123\n", "", nil)                // RunContainer
	m.AddResult("", "", nil)                        // CopyToContainer
	m.AddResult("", "", nil)                        // Exec chown
	m.AddResult("", "", fmt.Errorf("chmod failed")) // Exec chmod
	m.AddResult("", "", nil)                        // KillContainer (defer)
	m.AddResult("", "", nil)                        // RemoveContainer (defer)
	c := docker.NewClient(&m)

	err := WriteMungeKey(t.Context(), c, mesh.DefaultRealm, "dev", []byte("key"), "busybox:latest", false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "fixing munge key permissions")
}

// --- controllerImage ---

func TestControllerImage(t *testing.T) {
	cfg := &config.Cluster{
		Nodes: []config.Node{
			{Role: "worker", Image: "compute:1"},
			{Role: "controller", Image: "ctrl:1"},
		},
	}
	assert.Equal(t, "ctrl:1", controllerImage(cfg))
}

func TestControllerImage_Fallback(t *testing.T) {
	cfg := &config.Cluster{
		Nodes: []config.Node{
			{Role: "worker", Image: "compute:1"},
		},
	}
	assert.Equal(t, config.DefaultImage, controllerImage(cfg))
}

// --- NodeRunConfigs ---

func TestNodeRunConfigs_Minimal(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: "controller", Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: "worker", Count: 1, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "172.18.0.2", "25.11.0")

	require.Len(t, configs, 2)
	assert.Equal(t, "controller", configs[0].ShortName)
	assert.Equal(t, "controller", configs[0].Role)
	assert.Equal(t, "worker-0", configs[1].ShortName)
	assert.Equal(t, "worker", configs[1].Role)
	assert.True(t, configs[1].Managed, "worker defaults to managed")
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
			{Role: "worker", Count: 2, Image: "img:1", CPUs: 4, Memory: "8g", TmpSize: "1g"},
			{Role: "worker", Count: 1, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "", "")

	require.Len(t, configs, 4)
	assert.Equal(t, "worker-0", configs[1].ShortName)
	assert.Equal(t, 4, configs[1].CPUs)
	assert.Equal(t, "worker-1", configs[2].ShortName)
	assert.Equal(t, 4, configs[2].CPUs)
	assert.Equal(t, "worker-2", configs[3].ShortName)
	assert.Equal(t, 2, configs[3].CPUs)
}

func TestNodeRunConfigs_WithSubmitter(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: "controller", Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: "submitter", Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: "worker", Count: 1, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "", "")

	require.Len(t, configs, 3)
	assert.Equal(t, "controller", configs[0].ShortName)
	assert.Equal(t, "submitter", configs[1].ShortName)
	assert.Equal(t, "worker-0", configs[2].ShortName)
}

func TestNodeRunConfigs_ComputeDefaultCount(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: "controller", Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: "worker", Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "", "")

	require.Len(t, configs, 2)
	assert.Equal(t, "worker-0", configs[1].ShortName)
}

func TestNodeRunConfigs_UnmanagedCompute(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: "controller", Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: "worker", Count: 2, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g",
				Managed: boolPtr(false)},
			{Role: "worker", Count: 1, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "", "")

	require.Len(t, configs, 4)
	assert.False(t, configs[1].Managed, "worker-0 unmanaged")
	assert.False(t, configs[2].Managed, "worker-1 unmanaged")
	assert.True(t, configs[3].Managed, "worker-2 managed")
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

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "", "")

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

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "", "")

	require.Len(t, configs, 1)
	assert.Empty(t, configs[0].DataHostPath)
	assert.Empty(t, configs[0].DataMountPath)
}

func TestNodeRunConfigs_VolumeStorageCustomMount(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Storage: config.Storage{
			DataStorage: config.DataStorage{
				MountPath: "/shared",
			},
		},
		Nodes: []config.Node{
			{Role: "controller", Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "", "")

	require.Len(t, configs, 1)
	assert.Empty(t, configs[0].DataHostPath, "volume type uses docker volume, not host path")
	assert.Equal(t, "/shared", configs[0].DataMountPath)
}

func TestNodeRunConfigs_EmptyNodes(t *testing.T) {
	cfg := &config.Cluster{Name: "dev"}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "172.18.0.2", "25.11.0")

	assert.Empty(t, configs)
}

// --- CreateClusterNodes ---

func TestCreateClusterNodes(t *testing.T) {
	var m cmdexec.MockExecutor
	// Node 1: CreateContainer + ConnectNetwork + StartContainer
	m.AddResult("id1\n", "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	// Node 2: CreateContainer + ConnectNetwork + StartContainer
	m.AddResult("id2\n", "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	configs := []RunConfig{
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: "controller",
			Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "worker-0", Role: "worker",
			Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
	}

	err := CreateClusterNodes(t.Context(), c, mgr, configs)

	require.NoError(t, err)
	assert.Len(t, m.Calls, 6) // 2 nodes × 3 calls each
}

func TestCreateClusterNodes_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	// Node 1: success
	m.AddResult("id1\n", "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	// Node 2: CreateContainer fails
	m.AddResult("", "", fmt.Errorf("image not found"))
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	configs := []RunConfig{
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: "controller",
			Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "worker-0", Role: "worker",
			Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
	}

	err := CreateClusterNodes(t.Context(), c, mgr, configs)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "worker-0")
	assert.Len(t, m.Calls, 4) // 3 (node1) + 1 (node2 fails on create)
}

func TestCreateClusterNodes_Empty(t *testing.T) {
	var m cmdexec.MockExecutor
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := CreateClusterNodes(t.Context(), c, mgr, nil)

	require.NoError(t, err)
	assert.Empty(t, m.Calls)
}

// --- EnableSlurmServices ---

func TestEnableSlurmServices(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil) // slurmctld on controller
	m.AddResult("", "", nil) // slurmd on worker-0
	c := docker.NewClient(&m)

	configs := []RunConfig{
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: "controller"},
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "worker-0", Role: "worker", Managed: true},
	}

	err := EnableSlurmServices(t.Context(), c, configs)

	require.NoError(t, err)
	require.Len(t, m.Calls, 2)
	assert.Equal(t, []string{"exec", "sind-dev-controller", "systemctl", "enable", "--now", "slurmctld"},
		m.Calls[0].Args)
	assert.Equal(t, []string{"exec", "sind-dev-worker-0", "systemctl", "enable", "--now", "slurmd"},
		m.Calls[1].Args)
}

func TestEnableSlurmServices_SkipsSubmitter(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil) // slurmctld on controller
	c := docker.NewClient(&m)

	configs := []RunConfig{
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: "controller"},
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "submitter", Role: "submitter"},
	}

	err := EnableSlurmServices(t.Context(), c, configs)

	require.NoError(t, err)
	assert.Len(t, m.Calls, 1) // only controller
}

func TestEnableSlurmServices_SkipsUnmanaged(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil) // slurmctld on controller
	c := docker.NewClient(&m)

	configs := []RunConfig{
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: "controller"},
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "worker-0", Role: "worker", Managed: false},
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "worker-1", Role: "worker", Managed: true},
	}

	// Need result for worker-1 slurmd
	m.AddResult("", "", nil)

	err := EnableSlurmServices(t.Context(), c, configs)

	require.NoError(t, err)
	require.Len(t, m.Calls, 2)
	// Controller + worker-1 only; worker-0 skipped
	assert.Contains(t, m.Calls[1].Args, "sind-dev-worker-1")
}

func TestEnableSlurmServices_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", fmt.Errorf("systemctl failed"))
	c := docker.NewClient(&m)

	configs := []RunConfig{
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: "controller"},
	}

	err := EnableSlurmServices(t.Context(), c, configs)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "enabling slurmctld on controller")
}

// --- Create (end-to-end) ---

// inspectJSONLabels builds docker inspect JSON output with custom labels.
func inspectJSONLabels(t *testing.T, name, status string, networks map[docker.NetworkName]string, labels map[string]string) string {
	t.Helper()
	type netInfo struct {
		IPAddress string `json:"IPAddress"`
	}
	type result struct {
		ID    string `json:"Id"`
		Name  string `json:"Name"`
		State struct {
			Status string `json:"Status"`
		} `json:"State"`
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		NetworkSettings struct {
			Networks map[string]netInfo `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	r := result{ID: "id-" + name, Name: "/" + name}
	r.State.Status = status
	r.Config.Labels = labels
	nets := make(map[string]netInfo)
	for n, ip := range networks {
		nets[string(n)] = netInfo{IPAddress: ip}
	}
	r.NetworkSettings.Networks = nets
	data, err := json.Marshal([]result{r})
	require.NoError(t, err)
	return string(data)
}

// inspectJSON builds the JSON output that docker inspect returns for a container.
func inspectJSON(t *testing.T, name, status string, networks map[docker.NetworkName]string) string {
	t.Helper()
	return inspectJSONLabels(t, name, status, networks, map[string]string{})
}

// notFoundErr returns an exec.ExitError with exit code 1 for "not found" mocking.
func notFoundErr(t *testing.T) *exec.ExitError {
	t.Helper()
	cmd := exec.Command("sh", "-c", "exit 1")
	err := cmd.Run()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	return exitErr
}

// tarArchive builds a tar archive containing a single file with the given name and content.
func tarArchive(name, content string) string {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(content)), Mode: 0644})
	_, _ = tw.Write([]byte(content))
	_ = tw.Close()
	return buf.String()
}

// emptyCorefileContent returns a Corefile with no host entries.
func emptyCorefileContent() string {
	return "sind.sind:53 {\n    hosts {\n        fallthrough\n    }\n    log\n    errors\n}\n\n.:53 {\n    forward . /etc/resolv.conf\n    log\n    errors\n}\n"
}

// emptyCorefileTar returns a tar archive containing an empty Corefile (no host entries).
func emptyCorefileTar() string {
	return tarArchive("Corefile", emptyCorefileContent())
}

// happyOnCall returns an OnCall function that handles the full Create flow
// for a cluster with controller + 1 managed worker. The optional override
// function can intercept specific calls; returning (result, true) uses the
// override result, returning (_, false) falls through to the default.
func happyOnCall(t *testing.T, exitErr *exec.ExitError, override func(args []string, stdin string) (cmdexec.MockResult, bool)) func([]string, string) cmdexec.MockResult {
	t.Helper()
	return func(args []string, stdin string) cmdexec.MockResult {
		if override != nil {
			if r, ok := override(args, stdin); ok {
				return r
			}
		}
		joined := strings.Join(args, " ")

		// PreflightCheck: exists checks → "not found"
		if len(args) >= 2 && args[1] == "inspect" {
			switch args[0] {
			case "network", "volume", "container":
				return cmdexec.MockResult{Stderr: "Error: No such object\n", Err: exitErr}
			}
		}

		// resolveInfra
		if args[0] == "inspect" && args[1] == "sind-dns" {
			return cmdexec.MockResult{Stdout: inspectJSON(t, "sind-dns", "running", map[docker.NetworkName]string{
				"sind-dev-net": "10.0.0.2",
			})}
		}
		if args[0] == "exec" && args[1] == "sind-ssh" && len(args) > 2 && args[2] == "cat" {
			return cmdexec.MockResult{Stdout: "ssh-ed25519 AAAA-test-key\n"}
		}
		if args[0] == "run" && args[1] == "--rm" {
			return cmdexec.MockResult{Stdout: "slurm 25.11.0\n"}
		}

		// createResources
		if args[0] == "network" && args[1] == "create" {
			return cmdexec.MockResult{Stdout: "net-id\n"}
		}
		if args[0] == "volume" && args[1] == "create" {
			return cmdexec.MockResult{}
		}
		if args[0] == "create" {
			return cmdexec.MockResult{Stdout: "cid\n"}
		}
		if args[0] == "run" {
			return cmdexec.MockResult{Stdout: "cid\n"}
		}
		if args[0] == "cp" {
			if len(args) == 3 && args[2] == "-" && strings.Contains(args[1], ":") {
				return cmdexec.MockResult{Stdout: emptyCorefileTar()}
			}
			return cmdexec.MockResult{}
		}
		if args[0] == "rm" {
			return cmdexec.MockResult{}
		}
		if args[0] == "network" && args[1] == "connect" {
			return cmdexec.MockResult{}
		}
		if args[0] == "start" {
			return cmdexec.MockResult{}
		}
		if args[0] == "inspect" {
			return cmdexec.MockResult{Stdout: inspectJSON(t, args[1], "running", map[docker.NetworkName]string{
				"sind-dev-net": "10.0.1.1",
			})}
		}
		if args[0] == "exec" {
			if args[1] == "-i" {
				return cmdexec.MockResult{}
			}
			container := args[1]
			if len(args) > 2 {
				switch cmd := args[2]; {
				case cmd == "sh" && strings.Contains(joined, "is-system-running"):
					return cmdexec.MockResult{Stdout: "running\n"}
				case cmd == "bash" && strings.Contains(joined, "/dev/tcp"):
					return cmdexec.MockResult{Stdout: "SSH-2.0-OpenSSH_9.0\n"}
				case cmd == "mkdir":
					return cmdexec.MockResult{}
				case cmd == "ssh-keyscan":
					return cmdexec.MockResult{Stdout: "localhost ssh-ed25519 AAAA-hostkey-" + container + "\n"}
				case cmd == "systemctl" && len(args) > 3 && args[3] == "enable":
					return cmdexec.MockResult{}
				case cmd == "scontrol":
					return cmdexec.MockResult{Stdout: "Slurmctld(primary) at controller is UP\n"}
				case cmd == "systemctl" && len(args) > 3 && args[3] == "is-active":
					return cmdexec.MockResult{Stdout: "active\n"}
				}
			}
			return cmdexec.MockResult{}
		}
		if args[0] == "kill" {
			return cmdexec.MockResult{}
		}
		t.Errorf("unexpected docker call: %v", args)
		return cmdexec.MockResult{Err: fmt.Errorf("unexpected call: %v", args)}
	}
}

// createCfg returns a minimal cluster config with 1 controller + 1 managed worker.
func createCfg() *config.Cluster {
	return &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: "controller", Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: "worker", Count: 1, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}
}

func TestCreate_FullCluster(t *testing.T) {
	exitErr := notFoundErr(t)

	var m cmdexec.MockExecutor
	m.OnCall = happyOnCall(t, exitErr, nil)

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	cluster, err := Create(ctx, client, meshMgr, createCfg(), 1*time.Millisecond)

	require.NoError(t, err)
	require.NotNil(t, cluster)
	assert.Equal(t, "dev", cluster.Name)
	assert.Equal(t, "25.11.0", cluster.SlurmVersion)
	assert.Equal(t, StateRunning, cluster.State)
	require.Len(t, cluster.Nodes, 2)
	assert.Equal(t, "controller", cluster.Nodes[0].Name)
	assert.Equal(t, "controller", cluster.Nodes[0].Role)
	assert.Equal(t, "worker-0", cluster.Nodes[1].Name)
	assert.Equal(t, "worker", cluster.Nodes[1].Role)
	assert.Equal(t, StateRunning, cluster.Nodes[0].State)
	assert.Equal(t, StateRunning, cluster.Nodes[1].State)
}

func TestCreate_PreflightFails(t *testing.T) {
	// Network already exists → preflight returns conflict error.
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil) // network exists (no error = exists)
	exitErr := notFoundErr(t)
	for i := 0; i < 5; i++ {
		m.AddResult("", "Error: No such object\n", exitErr)
	}
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	cluster, err := Create(t.Context(), client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "conflicting resources")
	assert.Nil(t, cluster)
}

func TestCreate_ResolveInfraFails(t *testing.T) {
	exitErr := notFoundErr(t)
	var m cmdexec.MockExecutor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (cmdexec.MockResult, bool) {
		if args[0] == "inspect" && args[1] == "sind-dns" {
			return cmdexec.MockResult{Err: fmt.Errorf("container not running")}, true
		}
		return cmdexec.MockResult{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	cluster, err := Create(t.Context(), client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspecting DNS container")
	assert.Nil(t, cluster)
}

func TestCreate_CreateResourcesFails(t *testing.T) {
	exitErr := notFoundErr(t)
	var m cmdexec.MockExecutor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (cmdexec.MockResult, bool) {
		if args[0] == "network" && args[1] == "create" {
			return cmdexec.MockResult{Err: fmt.Errorf("network quota exceeded")}, true
		}
		return cmdexec.MockResult{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	cluster, err := Create(t.Context(), client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating cluster network")
	assert.Nil(t, cluster)
}

func TestCreate_SSHRelayConnectFails(t *testing.T) {
	exitErr := notFoundErr(t)
	connectCount := 0
	var m cmdexec.MockExecutor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (cmdexec.MockResult, bool) {
		// First network connect is SSH relay → cluster network; fail it.
		if args[0] == "network" && args[1] == "connect" {
			connectCount++
			if connectCount == 1 {
				return cmdexec.MockResult{Err: fmt.Errorf("network connect denied")}, true
			}
		}
		return cmdexec.MockResult{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	cluster, err := Create(t.Context(), client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting SSH relay")
	assert.Nil(t, cluster)
}

func TestCreate_NodeCreationFails(t *testing.T) {
	exitErr := notFoundErr(t)
	var m cmdexec.MockExecutor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (cmdexec.MockResult, bool) {
		// Let helper containers succeed, fail node containers
		if args[0] == "create" && !strings.Contains(strings.Join(args, " "), "helper") && !strings.Contains(strings.Join(args, " "), "busybox") {
			return cmdexec.MockResult{Err: fmt.Errorf("image not found")}, true
		}
		return cmdexec.MockResult{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	cluster, err := Create(t.Context(), client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "node")
	assert.Nil(t, cluster)
}

func TestCreate_SetupNodesFails(t *testing.T) {
	exitErr := notFoundErr(t)
	var m cmdexec.MockExecutor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (cmdexec.MockResult, bool) {
		// Probes inspect the node: return "created" instead of "running" to fail ContainerRunning
		if args[0] == "inspect" && strings.HasPrefix(args[1], "sind-dev-") {
			return cmdexec.MockResult{Stdout: inspectJSON(t, args[1], "created", map[docker.NetworkName]string{
				"sind-dev-net": "10.0.1.1",
			})}, true
		}
		return cmdexec.MockResult{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	cluster, err := Create(ctx, client, meshMgr, createCfg(), 10*time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "waiting for")
	assert.Nil(t, cluster)
}

func TestCreate_RegisterMeshFails(t *testing.T) {
	exitErr := notFoundErr(t)
	var m cmdexec.MockExecutor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (cmdexec.MockResult, bool) {
		// CopyFromContainer for Corefile fails
		if args[0] == "cp" && len(args) == 3 && args[2] == "-" && strings.Contains(args[1], "sind-dns") {
			return cmdexec.MockResult{Err: fmt.Errorf("dns container stopped")}, true
		}
		return cmdexec.MockResult{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	cluster, err := Create(ctx, client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "registering DNS")
	assert.Nil(t, cluster)
}

func TestCreate_EnableSlurmFails(t *testing.T) {
	exitErr := notFoundErr(t)
	var m cmdexec.MockExecutor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (cmdexec.MockResult, bool) {
		if args[0] == "exec" && len(args) > 3 && args[2] == "systemctl" && args[3] == "enable" {
			return cmdexec.MockResult{Err: fmt.Errorf("systemctl failed")}, true
		}
		return cmdexec.MockResult{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	cluster, err := Create(ctx, client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "enabling")
	assert.Nil(t, cluster)
}

func TestCreate_UnmanagedComputeSkipsSlurm(t *testing.T) {
	exitErr := notFoundErr(t)

	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: "controller", Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: "worker", Count: 1, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g",
				Managed: boolPtr(false)},
		},
	}

	var slurmCmds []string
	var m cmdexec.MockExecutor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (cmdexec.MockResult, bool) {
		if args[0] == "exec" && len(args) > 3 && args[2] == "systemctl" && args[3] == "enable" {
			slurmCmds = append(slurmCmds, args[1])
		}
		return cmdexec.MockResult{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	cluster, err := Create(ctx, client, meshMgr, cfg, time.Millisecond)

	require.NoError(t, err)
	require.Len(t, cluster.Nodes, 2)
	// Only controller should get slurm enabled, not unmanaged worker-0.
	assert.Equal(t, []string{"sind-dev-controller"}, slurmCmds)
}

func TestCreate_SubmitterSkipsSlurm(t *testing.T) {
	exitErr := notFoundErr(t)

	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: "controller", Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: "submitter", Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	var slurmCmds []string
	var m cmdexec.MockExecutor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (cmdexec.MockResult, bool) {
		if args[0] == "exec" && len(args) > 3 && args[2] == "systemctl" && args[3] == "enable" {
			slurmCmds = append(slurmCmds, args[1])
		}
		return cmdexec.MockResult{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	cluster, err := Create(ctx, client, meshMgr, cfg, time.Millisecond)

	require.NoError(t, err)
	require.Len(t, cluster.Nodes, 2)
	assert.Equal(t, "submitter", cluster.Nodes[1].Name)
	// Only controller gets slurmctld; submitter is skipped entirely.
	assert.Equal(t, []string{"sind-dev-controller"}, slurmCmds)
}

// --- Direct tests for unexported helpers ---

func TestResolveInfra_SSHKeyError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		if args[0] == "inspect" && args[1] == "sind-dns" {
			return cmdexec.MockResult{Stdout: inspectJSON(t, "sind-dns", "running", map[docker.NetworkName]string{
				"sind-dev-net": "10.0.0.2",
			})}
		}
		if args[0] == "exec" && args[1] == "sind-ssh" {
			return cmdexec.MockResult{Err: fmt.Errorf("ssh container crashed")}
		}
		if args[0] == "run" && args[1] == "--rm" {
			return cmdexec.MockResult{Stdout: "slurm 25.11.0\n"}
		}
		return cmdexec.MockResult{}
	}
	client := docker.NewClient(&m)
	cfg := createCfg()

	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	_, _, _, err := resolveInfra(t.Context(), client, meshMgr, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading SSH public key")
}

func TestResolveInfra_SlurmVersionError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		if args[0] == "inspect" && args[1] == "sind-dns" {
			return cmdexec.MockResult{Stdout: inspectJSON(t, "sind-dns", "running", map[docker.NetworkName]string{
				"sind-dev-net": "10.0.0.2",
			})}
		}
		if args[0] == "exec" && args[1] == "sind-ssh" {
			return cmdexec.MockResult{Stdout: "ssh-ed25519 AAAA-key\n"}
		}
		if args[0] == "run" && args[1] == "--rm" {
			return cmdexec.MockResult{Err: fmt.Errorf("image pull failed")}
		}
		return cmdexec.MockResult{}
	}
	client := docker.NewClient(&m)

	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	_, _, _, err := resolveInfra(t.Context(), client, meshMgr, createCfg())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "discovering Slurm version")
}

func TestSetupNodes_InspectError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		if args[0] == "inspect" {
			// First call: ContainerRunning probe → running
			name := args[1]
			return cmdexec.MockResult{Stdout: inspectJSON(t, name, "running", nil)}
		}
		if args[0] == "exec" {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "is-system-running") {
				return cmdexec.MockResult{Stdout: "running\n"}
			}
			if strings.Contains(joined, "/dev/tcp") {
				return cmdexec.MockResult{Stdout: "SSH-2.0-OpenSSH_9.0\n"}
			}
		}
		return cmdexec.MockResult{}
	}

	// Override: after probes pass, the second inspect (for IP collection) fails.
	callCount := 0
	origOnCall := m.OnCall
	m.OnCall = func(args []string, stdin string) cmdexec.MockResult {
		if args[0] == "inspect" && strings.HasPrefix(args[1], "sind-dev-") {
			callCount++
			if callCount > 1 {
				return cmdexec.MockResult{Err: fmt.Errorf("inspect network error")}
			}
		}
		return origOnCall(args, stdin)
	}

	client := docker.NewClient(&m)
	configs := []RunConfig{{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: "controller"}}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := setupNodes(ctx, client, mesh.DefaultRealm, "dev", "ssh-key", configs, time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspecting node controller")
}

func TestSetupNodes_InjectKeyError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		if args[0] == "inspect" {
			return cmdexec.MockResult{Stdout: inspectJSON(t, args[1], "running", map[docker.NetworkName]string{
				"sind-dev-net": "10.0.1.1",
			})}
		}
		if args[0] == "exec" {
			joined := strings.Join(args, " ")
			switch {
			case strings.Contains(joined, "is-system-running"):
				return cmdexec.MockResult{Stdout: "running\n"}
			case strings.Contains(joined, "/dev/tcp"):
				return cmdexec.MockResult{Stdout: "SSH-2.0-OpenSSH_9.0\n"}
			case args[2] == "mkdir":
				return cmdexec.MockResult{Err: fmt.Errorf("permission denied")}
			}
		}
		return cmdexec.MockResult{}
	}

	client := docker.NewClient(&m)
	configs := []RunConfig{{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: "controller"}}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := setupNodes(ctx, client, mesh.DefaultRealm, "dev", "ssh-key", configs, time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "injecting SSH key")
}

func TestSetupNodes_HostKeyError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		if args[0] == "inspect" {
			return cmdexec.MockResult{Stdout: inspectJSON(t, args[1], "running", map[docker.NetworkName]string{
				"sind-dev-net": "10.0.1.1",
			})}
		}
		if args[0] == "exec" {
			if args[1] == "-i" {
				return cmdexec.MockResult{}
			}
			joined := strings.Join(args, " ")
			switch {
			case strings.Contains(joined, "is-system-running"):
				return cmdexec.MockResult{Stdout: "running\n"}
			case strings.Contains(joined, "/dev/tcp"):
				return cmdexec.MockResult{Stdout: "SSH-2.0-OpenSSH_9.0\n"}
			case args[2] == "mkdir":
				return cmdexec.MockResult{}
			case args[2] == "ssh-keyscan":
				return cmdexec.MockResult{Err: fmt.Errorf("keyscan failed")}
			}
		}
		return cmdexec.MockResult{}
	}

	client := docker.NewClient(&m)
	configs := []RunConfig{{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: "controller"}}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := setupNodes(ctx, client, mesh.DefaultRealm, "dev", "ssh-key", configs, time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "collecting host key")
}

func TestRegisterMesh_DNSError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		// CopyFromContainer (read Corefile) fails
		if args[0] == "cp" && len(args) == 3 && args[2] == "-" {
			return cmdexec.MockResult{Err: fmt.Errorf("dns container not running")}
		}
		return cmdexec.MockResult{}
	}

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	configs := []RunConfig{{ClusterName: "dev", ShortName: "controller", Role: "controller"}}
	results := []nodeResult{{
		info:    &docker.ContainerInfo{ID: "id1", IPs: map[docker.NetworkName]string{"sind-dev-net": "10.0.1.1"}},
		hostKey: "ssh-ed25519 AAAA-key",
	}}

	_, err := registerMesh(t.Context(), meshMgr, "dev", "25.11.0", configs, results)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "registering DNS")
}

func TestRegisterMesh_KnownHostError(t *testing.T) {
	var m cmdexec.MockExecutor
	callIdx := 0
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		// CopyFromContainer (read Corefile)
		if args[0] == "cp" && len(args) == 3 && args[2] == "-" {
			return cmdexec.MockResult{Stdout: emptyCorefileTar()}
		}
		// CopyToContainer (write Corefile)
		if args[0] == "cp" && args[1] == "-" {
			return cmdexec.MockResult{}
		}
		// SignalContainer
		if args[0] == "kill" {
			return cmdexec.MockResult{}
		}
		// AppendFile → ExecWithStdin (exec -i)
		if args[0] == "exec" && args[1] == "-i" {
			callIdx++
			if callIdx == 1 {
				return cmdexec.MockResult{Err: fmt.Errorf("container stopped")}
			}
			return cmdexec.MockResult{}
		}
		return cmdexec.MockResult{}
	}

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	configs := []RunConfig{{ClusterName: "dev", ShortName: "controller", Role: "controller"}}
	results := []nodeResult{{
		info:    &docker.ContainerInfo{ID: "id1", IPs: map[docker.NetworkName]string{"sind-dev-net": "10.0.1.1"}},
		hostKey: "ssh-ed25519 AAAA-key",
	}}

	_, err := registerMesh(t.Context(), meshMgr, "dev", "25.11.0", configs, results)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "registering host key")
}

func TestEnableSlurm_ProbeTimeout(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		if args[0] == "exec" && len(args) > 3 && args[2] == "systemctl" && args[3] == "enable" {
			return cmdexec.MockResult{}
		}
		// scontrol ping always fails → probe times out
		if args[0] == "exec" && len(args) > 2 && args[2] == "scontrol" {
			return cmdexec.MockResult{Err: fmt.Errorf("slurmctld not responding")}
		}
		return cmdexec.MockResult{}
	}

	client := docker.NewClient(&m)
	configs := []RunConfig{{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: "controller"}}

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	err := enableSlurm(ctx, client, mesh.DefaultRealm, "dev", configs, 10*time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not ready")
}
