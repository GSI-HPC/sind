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

func TestNodeLabels(t *testing.T) {
	labels := NodeLabels(mesh.DefaultRealm, "dev", "controller", "25.11.0", "", 1)

	assert.Equal(t, map[string]string{
		"sind.realm":                              mesh.DefaultRealm,
		"sind.cluster":                            "dev",
		"sind.role":                               "controller",
		"sind.slurm.version":                      "25.11.0",
		"com.docker.compose.project":              "sind-dev",
		"com.docker.compose.service":              "controller",
		"com.docker.compose.container-number":     "1",
		"com.docker.compose.oneoff":               "False",
		"com.docker.compose.config-hash":          "",
		"com.docker.compose.project.config_files": "",
	}, labels)
}

func TestNodeLabels_NoSlurmVersion(t *testing.T) {
	labels := NodeLabels(mesh.DefaultRealm, "dev", "worker", "", "", 3)

	assert.Equal(t, map[string]string{
		"sind.realm":                              mesh.DefaultRealm,
		"sind.cluster":                            "dev",
		"sind.role":                               "worker",
		"com.docker.compose.project":              "sind-dev",
		"com.docker.compose.service":              "worker",
		"com.docker.compose.container-number":     "3",
		"com.docker.compose.oneoff":               "False",
		"com.docker.compose.config-hash":          "",
		"com.docker.compose.project.config_files": "",
	}, labels)
	_, ok := labels[LabelSlurmVersion]
	assert.False(t, ok, "slurm version label absent")
}

func TestNodeLabels_WithDataHostPath(t *testing.T) {
	labels := NodeLabels(mesh.DefaultRealm, "dev", "controller", "25.11.0", "/home/user/project", 1)

	assert.Equal(t, "/home/user/project", labels[LabelDataHostPath])
}

func TestNodeLabels_NoDataHostPath(t *testing.T) {
	labels := NodeLabels(mesh.DefaultRealm, "dev", "controller", "25.11.0", "", 1)

	_, ok := labels[LabelDataHostPath]
	assert.False(t, ok, "data host path label absent when empty")
}

func defaultRunConfig() RunConfig {
	return RunConfig{
		Realm:           mesh.DefaultRealm,
		ClusterName:     "dev",
		ShortName:       "controller",
		Role:            "controller",
		Image:           "ghcr.io/gsi-hpc/sind-node:25.11",
		CPUs:            2,
		Memory:          "2g",
		TmpSize:         "1g",
		SlurmVersion:    "25.11.0",
		DNSIP:           "172.18.0.2",
		ContainerNumber: 1,
	}
}

func TestBuildRunArgs_Basic(t *testing.T) {
	cfg := defaultRunConfig()
	args := BuildRunArgs(cfg)

	// Container name
	name, ok := testutil.ArgValue(args, "--name")
	assert.True(t, ok, "--name flag present")
	assert.Equal(t, "sind-dev-controller", name)

	// Hostname
	hostname, ok := testutil.ArgValue(args, "--hostname")
	assert.True(t, ok, "--hostname flag present")
	assert.Equal(t, "controller", hostname)

	// Image is last element
	assert.Equal(t, "ghcr.io/gsi-hpc/sind-node:25.11", args[len(args)-1])

	// Labels
	labels := testutil.ArgValues(args, "--label")
	assert.Contains(t, labels, "sind.realm="+mesh.DefaultRealm)
	assert.Contains(t, labels, "sind.cluster=dev")
	assert.Contains(t, labels, "sind.role=controller")
	assert.Contains(t, labels, "sind.slurm.version=25.11.0")
	assert.Contains(t, labels, "com.docker.compose.project=sind-dev")
	assert.Contains(t, labels, "com.docker.compose.service=controller")
	assert.Contains(t, labels, "com.docker.compose.container-number=1")
	assert.Contains(t, labels, "com.docker.compose.oneoff=False")
	assert.Contains(t, labels, "com.docker.compose.config-hash=")
	assert.Contains(t, labels, "com.docker.compose.project.config_files=")
}

func TestBuildRunArgs_ComputeNode(t *testing.T) {
	cfg := defaultRunConfig()
	cfg.ShortName = "worker-0"
	cfg.Role = "worker"
	args := BuildRunArgs(cfg)

	name, _ := testutil.ArgValue(args, "--name")
	assert.Equal(t, "sind-dev-worker-0", name)

	hostname, _ := testutil.ArgValue(args, "--hostname")
	assert.Equal(t, "worker-0", hostname)

	labels := testutil.ArgValues(args, "--label")
	assert.Contains(t, labels, "sind.role=worker")
}

func TestBuildRunArgs_NoSlurmVersion(t *testing.T) {
	cfg := defaultRunConfig()
	cfg.SlurmVersion = ""
	args := BuildRunArgs(cfg)

	labels := testutil.ArgValues(args, "--label")
	assert.Contains(t, labels, "sind.cluster=dev")
	assert.Contains(t, labels, "sind.role=controller")
	for _, l := range labels {
		assert.NotContains(t, l, "sind.slurm.version")
	}
}

func TestBuildRunArgs_Network(t *testing.T) {
	cfg := defaultRunConfig()
	args := BuildRunArgs(cfg)

	network, ok := testutil.ArgValue(args, "--network")
	assert.True(t, ok, "--network flag present")
	assert.Equal(t, "sind-dev-net", network)

	dns, ok := testutil.ArgValue(args, "--dns")
	assert.True(t, ok, "--dns flag present")
	assert.Equal(t, "172.18.0.2", dns)

	search, ok := testutil.ArgValue(args, "--dns-search")
	assert.True(t, ok, "--dns-search flag present")
	assert.Equal(t, "dev.sind.sind", search)
}

func TestBuildRunArgs_Network_NoDNSIP(t *testing.T) {
	cfg := defaultRunConfig()
	cfg.DNSIP = ""
	args := BuildRunArgs(cfg)

	_, ok := testutil.ArgValue(args, "--dns")
	assert.False(t, ok, "--dns flag absent when DNSIP empty")

	_, ok = testutil.ArgValue(args, "--dns-search")
	assert.True(t, ok, "--dns-search still present")
}

func TestBuildRunArgs_Mounts(t *testing.T) {
	tests := []struct {
		name     string
		role     string
		wantConf string
	}{
		{
			name:     "controller gets rw config",
			role:     "controller",
			wantConf: "sind-dev-config:/etc/slurm:rw",
		},
		{
			name:     "worker gets ro config",
			role:     "worker",
			wantConf: "sind-dev-config:/etc/slurm:ro",
		},
		{
			name:     "submitter gets ro config",
			role:     "submitter",
			wantConf: "sind-dev-config:/etc/slurm:ro",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultRunConfig()
			cfg.Role = tt.role
			cfg.ShortName = tt.role
			args := BuildRunArgs(cfg)

			volumes := testutil.ArgValues(args, "-v")
			assert.Contains(t, volumes, tt.wantConf)
			assert.Contains(t, volumes, "sind-dev-munge:/etc/munge:ro")
			assert.Contains(t, volumes, "sind-dev-data:/data:rw")
		})
	}
}

func TestBuildRunArgs_Mounts_HostPath(t *testing.T) {
	cfg := defaultRunConfig()
	cfg.DataHostPath = "/home/user/data"
	cfg.DataMountPath = "/shared"
	args := BuildRunArgs(cfg)

	volumes := testutil.ArgValues(args, "-v")
	assert.Contains(t, volumes, "/home/user/data:/shared:rw")
	for _, v := range volumes {
		assert.NotContains(t, v, "sind-dev-data")
	}
}

func TestBuildRunArgs_Mounts_DefaultDataPath(t *testing.T) {
	cfg := defaultRunConfig()
	args := BuildRunArgs(cfg)

	volumes := testutil.ArgValues(args, "-v")
	assert.Contains(t, volumes, "sind-dev-data:/data:rw")
}

func TestBuildRunArgs_Mounts_HostPathDefaultMount(t *testing.T) {
	cfg := defaultRunConfig()
	cfg.DataHostPath = "/home/user/data"
	// DataMountPath left empty — should default to /data
	args := BuildRunArgs(cfg)

	volumes := testutil.ArgValues(args, "-v")
	assert.Contains(t, volumes, "/home/user/data:/data:rw")
}

func TestBuildRunArgs_Mounts_CustomMountPath(t *testing.T) {
	cfg := defaultRunConfig()
	cfg.DataMountPath = "/shared"
	// DataHostPath left empty — should use docker volume
	args := BuildRunArgs(cfg)

	volumes := testutil.ArgValues(args, "-v")
	assert.Contains(t, volumes, "sind-dev-data:/shared:rw")
}

func TestBuildRunArgs_Resources(t *testing.T) {
	cfg := defaultRunConfig()
	cfg.CPUs = 4
	cfg.Memory = "8g"
	cfg.TmpSize = "2g"
	args := BuildRunArgs(cfg)

	cpus, ok := testutil.ArgValue(args, "--cpus")
	assert.True(t, ok)
	assert.Equal(t, "4", cpus)

	memory, ok := testutil.ArgValue(args, "--memory")
	assert.True(t, ok)
	assert.Equal(t, "8g", memory)

	tmpfs := testutil.ArgValues(args, "--tmpfs")
	assert.Contains(t, tmpfs, "/tmp:rw,nosuid,nodev,size=2g")
	assert.Contains(t, tmpfs, "/run:exec,mode=755")
	assert.Contains(t, tmpfs, "/run/lock")
}

func TestBuildRunArgs_SecurityOpts(t *testing.T) {
	cfg := defaultRunConfig()
	args := BuildRunArgs(cfg)

	secOpts := testutil.ArgValues(args, "--security-opt")
	assert.Contains(t, secOpts, "writable-cgroups=true")
	assert.Contains(t, secOpts, "label=disable")

	// Private cgroup namespace for systemd
	cgroupns, ok := testutil.ArgValue(args, "--cgroupns")
	assert.True(t, ok)
	assert.Equal(t, "private", cgroupns)
}

func TestBuildRunArgs_Pull(t *testing.T) {
	cfg := defaultRunConfig()
	cfg.Pull = true
	args := BuildRunArgs(cfg)

	pull, ok := testutil.ArgValue(args, "--pull")
	assert.True(t, ok, "--pull flag present")
	assert.Equal(t, "always", pull)

	// Image is still the last element
	assert.Equal(t, cfg.Image, args[len(args)-1])
}

func TestBuildRunArgs_NoPull(t *testing.T) {
	cfg := defaultRunConfig()
	args := BuildRunArgs(cfg)

	_, ok := testutil.ArgValue(args, "--pull")
	assert.False(t, ok, "--pull flag absent by default")
}

func TestBuildRunArgs_DefaultCluster(t *testing.T) {
	cfg := defaultRunConfig()
	cfg.ClusterName = "default"
	args := BuildRunArgs(cfg)

	name, _ := testutil.ArgValue(args, "--name")
	assert.Equal(t, "sind-default-controller", name)

	network, _ := testutil.ArgValue(args, "--network")
	assert.Equal(t, "sind-default-net", network)

	search, _ := testutil.ArgValue(args, "--dns-search")
	assert.Equal(t, "default.sind.sind", search)
}

// --- CreateNode ---

func TestCreateNode(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("abc123\n", "", nil) // CreateContainer
	m.AddResult("", "", nil)         // ConnectNetwork
	m.AddResult("", "", nil)         // StartContainer

	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)
	cfg := defaultRunConfig()

	id, err := CreateNode(t.Context(), c, mgr, cfg)
	require.NoError(t, err)
	assert.Equal(t, docker.ContainerID("abc123"), id)

	require.Len(t, m.Calls, 3)

	// CreateContainer: first arg is "create", last is image
	assert.Equal(t, "create", m.Calls[0].Args[0])
	assert.Equal(t, "ghcr.io/gsi-hpc/sind-node:25.11", m.Calls[0].Args[len(m.Calls[0].Args)-1])

	// ConnectNetwork
	assert.Equal(t, []string{"network", "connect", "sind-mesh", "sind-dev-controller"}, m.Calls[1].Args)

	// StartContainer
	assert.Equal(t, []string{"start", "sind-dev-controller"}, m.Calls[2].Args)
}

func TestCreateNode_CreateError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", fmt.Errorf("image not found"))

	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)
	cfg := defaultRunConfig()

	_, err := CreateNode(t.Context(), c, mgr, cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating container")
	assert.Len(t, m.Calls, 1)
}

func TestCreateNode_ConnectError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("abc123\n", "", nil)             // CreateContainer
	m.AddResult("", "", fmt.Errorf("net error")) // ConnectNetwork

	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)
	cfg := defaultRunConfig()

	_, err := CreateNode(t.Context(), c, mgr, cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "connecting")
	assert.Contains(t, err.Error(), "mesh")
	assert.Len(t, m.Calls, 2)
}

func TestCreateNode_StartError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("abc123\n", "", nil)                // CreateContainer
	m.AddResult("", "", nil)                        // ConnectNetwork
	m.AddResult("", "", fmt.Errorf("start failed")) // StartContainer

	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)
	cfg := defaultRunConfig()

	_, err := CreateNode(t.Context(), c, mgr, cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "starting container")
	assert.Len(t, m.Calls, 3)
}
