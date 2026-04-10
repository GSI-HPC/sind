// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Resource Lifecycle ---

func TestClusterResourceLifecycle(t *testing.T) {
	c, rec := testutil.NewClient(t)
	ctx := t.Context()
	clusterName := "it-res"

	if !rec.IsIntegration() {
		// CreateClusterNetwork
		rec.AddResult("net-id\n", "", nil)
		// CreateClusterVolume × 3 (config, munge, data)
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
		_ = c.RemoveVolume(bg, VolumeName(mesh.DefaultRealm, clusterName, VolumeConfig))
		_ = c.RemoveVolume(bg, VolumeName(mesh.DefaultRealm, clusterName, VolumeMunge))
		_ = c.RemoveVolume(bg, VolumeName(mesh.DefaultRealm, clusterName, VolumeData))
		_ = c.RemoveNetwork(bg, NetworkName(mesh.DefaultRealm, clusterName))
	})

	// Create network.
	err := CreateClusterNetwork(ctx, c, mesh.DefaultRealm, clusterName)
	require.NoError(t, err)

	// Create volumes.
	for _, vtype := range []VolumeType{VolumeConfig, VolumeMunge, VolumeData} {
		err = CreateClusterVolume(ctx, c, mesh.DefaultRealm, clusterName, vtype)
		require.NoError(t, err)
	}

	// Write config.
	helperImage := "busybox:latest"
	if rec.IsIntegration() {
		helperImage = "ghcr.io/gsi-hpc/sind-node:latest"
	}
	cfg := &config.Cluster{
		Name: clusterName,
		Nodes: []config.Node{
			{Role: config.RoleController, CPUs: 2, Memory: "2g", Image: helperImage},
			{Role: config.RoleWorker, Count: 1, CPUs: 2, Memory: "2g", Image: helperImage},
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

	for _, vtype := range AllVolumeTypes {
		exists, err = c.VolumeExists(ctx, VolumeName(mesh.DefaultRealm, clusterName, vtype))
		require.NoError(t, err)
		assert.True(t, exists, vtype+" volume")
	}

	t.Logf("docker I/O:\n%s", rec.Dump())
}

// --- CreateClusterNetwork ---

func TestCreateClusterNetwork(t *testing.T) {
	var m mock.Executor
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
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("network already exists"))
	c := docker.NewClient(&m)

	err := CreateClusterNetwork(t.Context(), c, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating cluster network")
}

// --- CreateClusterVolume ---

func TestCreateClusterVolume(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	err := CreateClusterVolume(t.Context(), c, mesh.DefaultRealm, "dev", VolumeConfig)

	require.NoError(t, err)
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{
		"volume", "create",
		"--label", "com.docker.compose.project=sind-dev",
		"--label", "com.docker.compose.volume=config",
		"sind-dev-config",
	}, m.Calls[0].Args)
}

func TestCreateClusterVolume_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("volume create failed"))
	c := docker.NewClient(&m)

	err := CreateClusterVolume(t.Context(), c, mesh.DefaultRealm, "dev", VolumeMunge)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "munge")
}

// --- WriteClusterConfig ---

func TestWriteClusterConfig(t *testing.T) {
	var m mock.Executor
	m.AddResult("abc123\n", "", nil) // CreateContainer (helper)
	m.AddResult("", "", nil)         // CopyToContainer
	m.AddResult("", "", nil)         // RemoveContainer (defer)
	c := docker.NewClient(&m)

	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: config.RoleController},
			{Role: config.RoleWorker, Count: 2, CPUs: 2, Memory: "2g"},
		},
	}
	err := WriteClusterConfig(t.Context(), c, mesh.DefaultRealm, cfg, "busybox:latest", false)

	require.NoError(t, err)
	require.Len(t, m.Calls, 3)

	// CreateContainer mounts config volume and labels for cleanup
	assert.Equal(t, "create", m.Calls[0].Args[0])
	assert.Contains(t, m.Calls[0].Args, "sind-dev-config:/etc/slurm")
	assert.Contains(t, m.Calls[0].Args, "sind-dev-config-helper")
	assert.Contains(t, m.Calls[0].Args, LabelRealm+"=sind")
	assert.Contains(t, m.Calls[0].Args, LabelCluster+"=dev")

	// CopyToContainer writes to /etc/slurm
	assert.Equal(t, "cp", m.Calls[1].Args[0])
	assert.Contains(t, m.Calls[1].Args[len(m.Calls[1].Args)-1], "sind-dev-config-helper:/etc/slurm")

	// RemoveContainer cleans up
	assert.Equal(t, []string{"rm", "-f", "sind-dev-config-helper"}, m.Calls[2].Args)
}

func TestWriteClusterConfig_MainStringAppend(t *testing.T) {
	var m mock.Executor
	m.AddResult("abc123\n", "", nil) // CreateContainer (helper)
	m.AddResult("", "", nil)         // CopyToContainer
	m.AddResult("", "", nil)         // RemoveContainer (defer)
	c := docker.NewClient(&m)

	cfg := &config.Cluster{
		Name: "dev",
		Slurm: config.Slurm{
			Main: config.Section{Content: "SchedulerType=sched/backfill\n"},
		},
		Nodes: []config.Node{
			{Role: "controller"},
			{Role: "worker", Count: 2, CPUs: 2, Memory: "2g"},
		},
	}
	err := WriteClusterConfig(t.Context(), c, mesh.DefaultRealm, cfg, "busybox:latest", false)

	require.NoError(t, err)
	cpStdin := m.Calls[1].Stdin
	assert.Contains(t, cpStdin, "SchedulerType=sched/backfill")
}

func TestWriteClusterConfig_MainMapFragments(t *testing.T) {
	var m mock.Executor
	m.AddResult("abc123\n", "", nil) // CreateContainer (helper)
	m.AddResult("", "", nil)         // CopyToContainer
	m.AddResult("", "", nil)         // RemoveContainer (defer)
	c := docker.NewClient(&m)

	cfg := &config.Cluster{
		Name: "dev",
		Slurm: config.Slurm{
			Main: config.Section{Fragments: map[string]string{
				"scheduling": "SchedulerType=sched/backfill\n",
				"resources":  "SelectType=select/cons_tres\n",
			}},
		},
		Nodes: []config.Node{
			{Role: "controller"},
			{Role: "worker", Count: 2, CPUs: 2, Memory: "2g"},
		},
	}
	err := WriteClusterConfig(t.Context(), c, mesh.DefaultRealm, cfg, "busybox:latest", false)

	require.NoError(t, err)
	cpStdin := m.Calls[1].Stdin
	assert.Contains(t, cpStdin, "slurm.conf.d/")
	assert.Contains(t, cpStdin, "slurm.conf.d/scheduling.conf")
	assert.Contains(t, cpStdin, "slurm.conf.d/resources.conf")
	assert.Contains(t, cpStdin, "SchedulerType=sched/backfill")
	assert.Contains(t, cpStdin, "SelectType=select/cons_tres")
}

func TestWriteClusterConfig_PlugstackAlwaysScaffolded(t *testing.T) {
	var m mock.Executor
	m.AddResult("abc123\n", "", nil) // CreateContainer (helper)
	m.AddResult("", "", nil)         // CopyToContainer
	m.AddResult("", "", nil)         // RemoveContainer (defer)
	c := docker.NewClient(&m)

	cfg := &config.Cluster{
		Name:  "dev",
		Nodes: []config.Node{{Role: "controller"}, {Role: "worker"}},
	}
	err := WriteClusterConfig(t.Context(), c, mesh.DefaultRealm, cfg, "busybox:latest", false)

	require.NoError(t, err)
	cpStdin := m.Calls[1].Stdin
	assert.Contains(t, cpStdin, "plugstack.conf")
	assert.Contains(t, cpStdin, "plugstack.conf.d/")
}

func TestWriteClusterConfig_GresSection(t *testing.T) {
	var m mock.Executor
	m.AddResult("abc123\n", "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	cfg := &config.Cluster{
		Name: "dev",
		Slurm: config.Slurm{
			Gres: config.Section{Content: "Name=gpu Type=tesla\n"},
		},
		Nodes: []config.Node{{Role: "controller"}, {Role: "worker"}},
	}
	err := WriteClusterConfig(t.Context(), c, mesh.DefaultRealm, cfg, "busybox:latest", false)

	require.NoError(t, err)
	cpStdin := m.Calls[1].Stdin
	assert.Contains(t, cpStdin, "gres.conf")
	assert.Contains(t, cpStdin, "Name=gpu Type=tesla")
}

func TestWriteClusterConfig_Pull(t *testing.T) {
	var m mock.Executor
	m.AddResult("abc123\n", "", nil) // CreateContainer (helper)
	m.AddResult("", "", nil)         // CopyToContainer
	m.AddResult("", "", nil)         // RemoveContainer (defer)
	c := docker.NewClient(&m)

	cfg := &config.Cluster{Name: "dev"}
	err := WriteClusterConfig(t.Context(), c, mesh.DefaultRealm, cfg, "busybox:latest", true)

	require.NoError(t, err)
	createArgs := m.Calls[0].Args
	pull, ok := testutil.ArgValue(createArgs, "--pull")
	assert.True(t, ok, "--pull flag present")
	assert.Equal(t, "always", pull)
}

func TestWriteClusterConfig_WithDb(t *testing.T) {
	var m mock.Executor
	m.AddResult("abc123\n", "", nil) // CreateContainer (helper)
	m.AddResult("", "", nil)         // CopyToContainer
	m.AddResult("", "", nil)         // RemoveContainer (defer)
	c := docker.NewClient(&m)

	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: config.RoleController},
			{Role: config.RoleDb},
			{Role: config.RoleWorker},
		},
	}
	err := WriteClusterConfig(t.Context(), c, mesh.DefaultRealm, cfg, "busybox:latest", false)

	require.NoError(t, err)

	// Verify the CopyToContainer call includes slurm.conf with accounting directives
	cpCall := m.Calls[1]
	assert.Equal(t, "cp", cpCall.Args[0])
}

func TestWriteClusterConfig_CreateError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("create failed"))
	c := docker.NewClient(&m)

	cfg := &config.Cluster{Name: "dev"}
	err := WriteClusterConfig(t.Context(), c, mesh.DefaultRealm, cfg, "busybox:latest", false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating config helper")
}

func TestWriteClusterConfig_CopyError(t *testing.T) {
	var m mock.Executor
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
	var m mock.Executor
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

	// RunContainer mounts munge volume and labels for cleanup
	assert.Equal(t, "run", m.Calls[0].Args[0])
	assert.Contains(t, m.Calls[0].Args, "sind-dev-munge:/etc/munge")
	assert.Contains(t, m.Calls[0].Args, LabelRealm+"=sind")
	assert.Contains(t, m.Calls[0].Args, LabelCluster+"=dev")

	// CopyToContainer + chown + chmod
	assert.Equal(t, "cp", m.Calls[1].Args[0])
	assert.Equal(t, []string{"exec", "sind-dev-munge-helper", "chown", "munge:munge", "/etc/munge/munge.key"}, m.Calls[2].Args)
	assert.Equal(t, []string{"exec", "sind-dev-munge-helper", "chmod", "0400", "/etc/munge/munge.key"}, m.Calls[3].Args)
}

func TestWriteMungeKey_Pull(t *testing.T) {
	var m mock.Executor
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
	pull, ok := testutil.ArgValue(runArgs, "--pull")
	assert.True(t, ok, "--pull flag present")
	assert.Equal(t, "always", pull)
}

func TestWriteMungeKey_RunError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("run failed"))
	c := docker.NewClient(&m)

	err := WriteMungeKey(t.Context(), c, mesh.DefaultRealm, "dev", []byte("key"), "busybox:latest", false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating munge helper")
}

func TestWriteMungeKey_CopyError(t *testing.T) {
	var m mock.Executor
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
	var m mock.Executor
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
	var m mock.Executor
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
