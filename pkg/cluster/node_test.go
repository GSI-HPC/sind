// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"fmt"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNodeLabels(t *testing.T) {
	labels := NodeLabels(mesh.DefaultRealm, "dev", config.RoleController, true, "25.11.0", "", 1)

	assert.Equal(t, docker.Labels{
		"sind.realm":                              mesh.DefaultRealm,
		"sind.cluster":                            "dev",
		"sind.role":                               "controller",
		"sind.managed":                            "true",
		"sind.slurm.version":                      "25.11.0",
		"sind.data.hostpath":                      "",
		"com.docker.compose.project":              "sind-dev",
		"com.docker.compose.service":              "controller",
		"com.docker.compose.container-number":     "1",
		"com.docker.compose.oneoff":               "False",
		"com.docker.compose.config-hash":          "",
		"com.docker.compose.project.config_files": "",
	}, labels)
}

func TestNodeLabels_NoSlurmVersion(t *testing.T) {
	labels := NodeLabels(mesh.DefaultRealm, "dev", config.RoleWorker, false, "", "", 3)

	assert.Equal(t, docker.Labels{
		"sind.realm":                              mesh.DefaultRealm,
		"sind.cluster":                            "dev",
		"sind.role":                               "worker",
		"sind.managed":                            "false",
		"sind.slurm.version":                      "",
		"sind.data.hostpath":                      "",
		"com.docker.compose.project":              "sind-dev",
		"com.docker.compose.service":              "worker",
		"com.docker.compose.container-number":     "3",
		"com.docker.compose.oneoff":               "False",
		"com.docker.compose.config-hash":          "",
		"com.docker.compose.project.config_files": "",
	}, labels, "empty slurm version label overrides the image's")
}

func TestNodeLabels_WithDataHostPath(t *testing.T) {
	labels := NodeLabels(mesh.DefaultRealm, "dev", config.RoleController, true, "25.11.0", "/home/user/project", 1)

	assert.Equal(t, "/home/user/project", labels[LabelDataHostPath])
}

func TestNodeLabels_NoDataHostPath(t *testing.T) {
	labels := NodeLabels(mesh.DefaultRealm, "dev", config.RoleController, true, "25.11.0", "", 1)

	hostPath, ok := labels[LabelDataHostPath]
	assert.True(t, ok, "set empty in volume mode, so that an image label cannot show through")
	assert.Empty(t, hostPath)
}

func TestIsManaged(t *testing.T) {
	assert.True(t, IsManaged(docker.Labels{LabelManaged: "true"}))
	assert.False(t, IsManaged(docker.Labels{LabelManaged: "false"}))
	assert.True(t, IsManaged(docker.Labels{LabelRole: "worker"}), "created before the label existed")
	assert.True(t, IsManaged(nil))
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
		Managed:         true,
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

	// The image runs the node entrypoint, which ends by starting systemd
	assert.Equal(t, []string{"--entrypoint", "/bin/sh", "ghcr.io/gsi-hpc/sind-node:25.11", "-c", NodeEntrypoint},
		args[len(args)-5:])

	// Labels
	labels := testutil.ArgValues(args, "--label")
	assert.Contains(t, labels, "sind.realm="+mesh.DefaultRealm)
	assert.Contains(t, labels, "sind.cluster=dev")
	assert.Contains(t, labels, "sind.role=controller")
	assert.Contains(t, labels, "sind.managed=true")
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
	cfg.Managed = false
	args := BuildRunArgs(cfg)

	name, _ := testutil.ArgValue(args, "--name")
	assert.Equal(t, "sind-dev-worker-0", name)

	hostname, _ := testutil.ArgValue(args, "--hostname")
	assert.Equal(t, "worker-0", hostname)

	labels := testutil.ArgValues(args, "--label")
	assert.Contains(t, labels, "sind.role=worker")
	assert.Contains(t, labels, "sind.managed=false")
}

func TestBuildRunArgs_NoSlurmVersion(t *testing.T) {
	cfg := defaultRunConfig()
	cfg.SlurmVersion = ""
	args := BuildRunArgs(cfg)

	// An empty label overrides the one the sind-node image carries.
	labels := testutil.ArgValues(args, "--label")
	assert.Contains(t, labels, "sind.cluster=dev")
	assert.Contains(t, labels, "sind.role=controller")
	assert.Contains(t, labels, "sind.slurm.version=")
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
		role     config.Role
		wantConf string
	}{
		{
			name:     "controller gets rw config",
			role:     config.RoleController,
			wantConf: "sind-dev-config:/etc/slurm:rw",
		},
		{
			name:     "worker gets ro config",
			role:     config.RoleWorker,
			wantConf: "sind-dev-config:/etc/slurm:ro",
		},
		{
			name:     "submitter gets ro config",
			role:     config.RoleSubmitter,
			wantConf: "sind-dev-config:/etc/slurm:ro",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultRunConfig()
			cfg.Role = tt.role
			cfg.ShortName = string(tt.role)
			args := BuildRunArgs(cfg)

			volumes := testutil.ArgValues(args, "-v")
			assert.Contains(t, volumes, tt.wantConf)
			assert.Contains(t, volumes, "sind-dev-munge:/etc/munge:ro")
			assert.Contains(t, volumes, "sind-dev-data:/data:rw")
		})
	}
}

func TestBuildRunArgs_Mounts_SharedState(t *testing.T) {
	cfg := defaultRunConfig()
	assert.NotContains(t, testutil.ArgValues(BuildRunArgs(cfg), "-v"), "sind-dev-state:/var/spool/slurmctld:rw")

	cfg.SharedState = true
	assert.Contains(t, testutil.ArgValues(BuildRunArgs(cfg), "-v"), "sind-dev-state:/var/spool/slurmctld:rw")
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

func TestBuildRunArgs_DataMountPathLabel(t *testing.T) {
	cfg := defaultRunConfig()
	cfg.DataMountPath = "/shared"

	assert.Contains(t, testutil.ArgValues(BuildRunArgs(cfg), "--label"), LabelDataMountPath+"=/shared")
}

func TestBuildRunArgs_DataMountPathLabelForDefault(t *testing.T) {
	for _, mountPath := range []string{"", DefaultDataMountPath} {
		cfg := defaultRunConfig()
		cfg.DataMountPath = mountPath

		assert.Contains(t, testutil.ArgValues(BuildRunArgs(cfg), "--label"), LabelDataMountPath+"=/data")
	}
}

// TestBuildRunArgs_ReadBackLabelsAlwaysSet checks that a node of a cluster
// that records nothing in them (volume mode, no CVMFS, no users, identity
// local) still gets every label sind reads back: Docker merges the image's
// labels into the container's, so a label left out could come from the
// image, e.g. sind.data.hostpath=/ for sind create worker to bind-mount.
func TestBuildRunArgs_ReadBackLabelsAlwaysSet(t *testing.T) {
	cfg := defaultRunConfig()
	labels := map[string]string{}
	for _, l := range testutil.ArgValues(BuildRunArgs(cfg), "--label") {
		k, v, _ := strings.Cut(l, "=")
		labels[k] = v
	}

	for key, want := range map[string]string{
		LabelSlurmVersion:  "25.11.0",
		LabelDataHostPath:  "",
		LabelDataMountPath: DefaultDataMountPath,
		LabelCVMFS:         "",
		LabelUsers:         "",
		LabelGroups:        "",
		LabelIdentity:      string(config.IdentityLocal),
	} {
		got, ok := labels[key]
		if assert.True(t, ok, "label %s set", key) {
			assert.Equal(t, want, got, key)
		}
	}
}

func TestBuildRunArgs_CVMFS(t *testing.T) {
	tests := []struct {
		backend   config.StorageType
		wantMount string
	}{
		{config.StorageVolume, "type=volume,volume-driver=cvmfs,source=cvmfs,target=/cvmfs,readonly"},
		{config.StorageHostPath, "type=bind,source=/cvmfs,target=/cvmfs,readonly,bind-propagation=rslave"},
	}
	for _, tt := range tests {
		t.Run(string(tt.backend), func(t *testing.T) {
			cfg := defaultRunConfig()
			cfg.CVMFS = tt.backend
			args := BuildRunArgs(cfg)

			assert.Equal(t, []string{tt.wantMount}, testutil.ArgValues(args, "--mount"))
			assert.Contains(t, testutil.ArgValues(args, "--label"), LabelCVMFS+"="+string(tt.backend))
		})
	}
}

func TestBuildRunArgs_NoCVMFS(t *testing.T) {
	args := BuildRunArgs(defaultRunConfig())

	assert.Empty(t, testutil.ArgValues(args, "--mount"))
	assert.Contains(t, testutil.ArgValues(args, "--label"), LabelCVMFS+"=")
}

func TestDataMountPath(t *testing.T) {
	assert.Equal(t, DefaultDataMountPath, DataMountPath(nil))
	assert.Equal(t, "/shared", DataMountPath(docker.Labels{LabelDataMountPath: "/shared"}))
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

	// The image and the entrypoint's arguments still come last
	assert.Equal(t, []string{cfg.Image, "-c", NodeEntrypoint}, args[len(args)-3:])
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
	var m mock.Executor
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

	// CreateContainer: first arg is "create", then the image and its entrypoint arguments
	assert.Equal(t, "create", m.Calls[0].Args[0])
	assert.Equal(t, []string{"ghcr.io/gsi-hpc/sind-node:25.11", "-c", NodeEntrypoint}, m.Calls[0].Args[len(m.Calls[0].Args)-3:])

	// ConnectNetwork
	assert.Equal(t, []string{"network", "connect", "sind-mesh", "sind-dev-controller"}, m.Calls[1].Args)

	// StartContainer
	assert.Equal(t, []string{"start", "sind-dev-controller"}, m.Calls[2].Args)
}

func TestCreateNode_CreateError(t *testing.T) {
	var m mock.Executor
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
	var m mock.Executor
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
	var m mock.Executor
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

// --- NodeRunConfigs ---

func TestNodeRunConfigs_Minimal(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: config.RoleController, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: config.RoleWorker, Count: 1, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "172.18.0.2", "25.11.0", "")

	require.Len(t, configs, 2)
	assert.Equal(t, "controller", configs[0].ShortName)
	assert.Equal(t, config.RoleController, configs[0].Role)
	assert.True(t, configs[0].Managed, "controller defaults to managed")
	assert.Equal(t, "worker-0", configs[1].ShortName)
	assert.Equal(t, config.RoleWorker, configs[1].Role)
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
			{Role: config.RoleController, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: config.RoleWorker, Count: 2, Image: "img:1", CPUs: 4, Memory: "8g", TmpSize: "1g"},
			{Role: config.RoleWorker, Count: 1, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "", "", "")

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
			{Role: config.RoleController, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: config.RoleSubmitter, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: config.RoleWorker, Count: 1, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "", "", "")

	require.Len(t, configs, 3)
	assert.Equal(t, "controller", configs[0].ShortName)
	assert.Equal(t, "submitter", configs[1].ShortName)
	assert.True(t, configs[1].Managed, "submitter of a managed cluster")
	assert.Equal(t, "worker-0", configs[2].ShortName)
}

func TestNodeRunConfigs_WithDB(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: config.RoleController},
			{Role: config.RoleDB, Image: "img:1", CPUs: 2, Memory: "1g", TmpSize: "1g"},
			{Role: config.RoleWorker},
		},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "10.0.0.2", "25.11.8", "")

	require.Len(t, configs, 3)
	db := configs[1]
	assert.Equal(t, "db", db.ShortName)
	assert.Equal(t, config.RoleDB, db.Role)
	assert.Equal(t, "img:1", db.Image)
	assert.Equal(t, 2, db.CPUs)
	assert.Equal(t, "1g", db.Memory)
	assert.Equal(t, "25.11.8", db.SlurmVersion)
	assert.Equal(t, 1, db.ContainerNumber)
	assert.True(t, db.Managed, "db of a managed cluster")
	assert.False(t, db.SharedState)
}

func TestNodeRunConfigs_UnmanagedDBInManagedCluster(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: config.RoleController},
			{Role: config.RoleDB, Managed: testutil.Ptr(false)},
			{Role: config.RoleSubmitter},
			{Role: config.RoleWorker},
		},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "", "25.11.8", "")

	require.Len(t, configs, 4)
	assert.True(t, configs[0].Managed, "controller")
	assert.Equal(t, "db", configs[1].ShortName)
	assert.False(t, configs[1].Managed, "db with managed: false")
	assert.True(t, configs[2].Managed, "submitter")
	assert.True(t, configs[3].Managed, "worker")
}

func TestNodeRunConfigs_UnmanagedDB(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: config.RoleController, Managed: testutil.Ptr(false)},
			{Role: config.RoleDB},
			{Role: config.RoleWorker},
		},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "", "", "")

	require.Len(t, configs, 3)
	assert.Equal(t, "db", configs[1].ShortName)
	assert.False(t, configs[1].Managed, "bare db node in an unmanaged cluster")
}

func TestNodeRunConfigs_ComputeDefaultCount(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: config.RoleController, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: config.RoleWorker, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "", "", "")

	require.Len(t, configs, 2)
	assert.Equal(t, "worker-0", configs[1].ShortName)
}

func TestNodeRunConfigs_UnmanagedCompute(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: config.RoleController, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: config.RoleWorker, Count: 2, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g",
				Managed: testutil.Ptr(false)},
			{Role: config.RoleWorker, Count: 1, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "", "", "")

	require.Len(t, configs, 4)
	assert.False(t, configs[1].Managed, "worker-0 unmanaged")
	assert.False(t, configs[2].Managed, "worker-1 unmanaged")
	assert.True(t, configs[3].Managed, "worker-2 managed")
}

func TestNodeRunConfigs_UnmanagedController(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: config.RoleController, Managed: testutil.Ptr(false), BackupController: true},
			{Role: config.RoleSubmitter},
			{Role: config.RoleWorker, Count: 2},
			{Role: config.RoleWorker, Managed: testutil.Ptr(false)},
		},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "", "", "")

	require.Len(t, configs, 6)
	for _, c := range configs {
		assert.False(t, c.Managed, "%s unmanaged", c.ShortName)
	}
	assert.Equal(t, "controller-backup", configs[1].ShortName)
	assert.True(t, configs[1].SharedState, "backup still shares the state volume")
}

func TestNodeRunConfigs_BackupController(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: config.RoleController, BackupController: true, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g",
				CapAdd: []string{"SYS_ADMIN"}, Devices: []string{"/dev/fuse"}},
			{Role: config.RoleWorker, Count: 1, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "172.18.0.2", "25.11.0", "")

	require.Len(t, configs, 3)
	primary, backup := configs[0], configs[1]
	assert.Equal(t, "controller", primary.ShortName)
	assert.Equal(t, 1, primary.ContainerNumber)
	assert.True(t, primary.SharedState)
	assert.Equal(t, "controller-backup", backup.ShortName)
	assert.Equal(t, 2, backup.ContainerNumber)
	assert.Equal(t, config.RoleController, backup.Role)

	// Apart from name and container number the two controllers are
	// identical: resources, volumes, caps, devices, security options.
	backup.ShortName = primary.ShortName
	backup.ContainerNumber = primary.ContainerNumber
	assert.Equal(t, primary, backup)

	assert.Equal(t, "worker-0", configs[2].ShortName)
	assert.False(t, configs[2].SharedState)
}

func TestNodeRunConfigs_CVMFS(t *testing.T) {
	// Every node mounts CVMFS: controllers of a backup pair, submitter and
	// all workers.
	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: config.RoleController, BackupController: true},
			{Role: config.RoleSubmitter},
			{Role: config.RoleWorker, Count: 2},
		},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "", "", config.StorageHostPath)

	require.Len(t, configs, 5)
	for _, c := range configs {
		assert.Equal(t, config.StorageHostPath, c.CVMFS, c.ShortName)
	}
}

func TestNodeRunConfigs_NoBackupController(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: config.RoleController},
			{Role: config.RoleWorker, Count: 1},
		},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "", "", "")

	require.Len(t, configs, 2)
	assert.Equal(t, "controller", configs[0].ShortName)
	assert.False(t, configs[0].SharedState)
}

func TestNodeRunConfigs_HostPathStorage(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Storage: config.Storage{
			DataStorage: config.DataStorage{
				Type:      config.StorageHostPath,
				HostPath:  "/data/shared",
				MountPath: "/shared",
			},
		},
		Nodes: []config.Node{
			{Role: config.RoleController, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "", "", "")

	require.Len(t, configs, 1)
	assert.Equal(t, "/data/shared", configs[0].DataHostPath)
	assert.Equal(t, "/shared", configs[0].DataMountPath)
}

func TestNodeRunConfigs_VolumeStorage(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: config.RoleController, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "", "", "")

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
			{Role: config.RoleController, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "", "", "")

	require.Len(t, configs, 1)
	assert.Empty(t, configs[0].DataHostPath, "volume type uses docker volume, not host path")
	assert.Equal(t, "/shared", configs[0].DataMountPath)
}

func TestNodeRunConfigs_StorageTypeDecidesTheMount(t *testing.T) {
	tests := []struct {
		name         string
		ds           config.DataStorage
		wantHostPath string
	}{
		{"volume ignores hostPath", config.DataStorage{Type: config.StorageVolume, HostPath: "/srv/data"}, ""},
		{"hostPath without type", config.DataStorage{HostPath: "/srv/data"}, "/srv/data"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Cluster{
				Name:    "dev",
				Storage: config.Storage{DataStorage: tt.ds},
				Nodes: []config.Node{
					{Role: config.RoleController, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
				},
			}

			configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "", "", "")

			require.Len(t, configs, 1)
			assert.Equal(t, tt.wantHostPath, configs[0].DataHostPath)
		})
	}
}

func TestNodeRunConfigs_EmptyNodes(t *testing.T) {
	cfg := &config.Cluster{Name: "dev"}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "172.18.0.2", "25.11.0", "")

	assert.Empty(t, configs)
}

// --- CreateClusterNodes ---

func TestCreateClusterNodes(t *testing.T) {
	var m mock.Executor
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
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: config.RoleController,
			Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "worker-0", Role: config.RoleWorker,
			Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
	}

	err := CreateClusterNodes(t.Context(), c, mgr, configs)

	require.NoError(t, err)
	assert.Len(t, m.Calls, 6) // 2 nodes × 3 calls each
}

func TestCreateClusterNodes_Error(t *testing.T) {
	var m mock.Executor
	// Node 1: success
	m.AddResult("id1\n", "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	// Node 2: CreateContainer fails
	m.AddResult("", "", fmt.Errorf("image not found"))
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	configs := []RunConfig{
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: config.RoleController,
			Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "worker-0", Role: config.RoleWorker,
			Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
	}

	err := CreateClusterNodes(t.Context(), c, mgr, configs)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "worker-0")
	assert.Len(t, m.Calls, 4) // 3 (node1) + 1 (node2 fails on create)
}

func TestCreateClusterNodes_Empty(t *testing.T) {
	var m mock.Executor
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := CreateClusterNodes(t.Context(), c, mgr, nil)

	require.NoError(t, err)
	assert.Empty(t, m.Calls)
}

// --- Security fields in BuildRunArgs ---

func TestBuildRunArgs_CapAdd(t *testing.T) {
	cfg := defaultRunConfig()
	cfg.CapAdd = []string{"SYS_ADMIN", "NET_ADMIN"}
	args := BuildRunArgs(cfg)

	caps := testutil.ArgValues(args, "--cap-add")
	assert.Equal(t, []string{"SYS_ADMIN", "NET_ADMIN"}, caps)
}

func TestBuildRunArgs_CapDrop(t *testing.T) {
	cfg := defaultRunConfig()
	cfg.CapDrop = []string{"MKNOD"}
	args := BuildRunArgs(cfg)

	caps := testutil.ArgValues(args, "--cap-drop")
	assert.Equal(t, []string{"MKNOD"}, caps)
}

func TestBuildRunArgs_Devices(t *testing.T) {
	cfg := defaultRunConfig()
	cfg.Devices = []string{"/dev/fuse", "/dev/sda:/dev/xvda:rwm"}
	args := BuildRunArgs(cfg)

	devs := testutil.ArgValues(args, "--device")
	assert.Equal(t, []string{"/dev/fuse", "/dev/sda:/dev/xvda:rwm"}, devs)
}

func TestBuildRunArgs_SecurityOptExtra(t *testing.T) {
	cfg := defaultRunConfig()
	cfg.SecurityOpt = []string{"apparmor=unconfined"}
	args := BuildRunArgs(cfg)

	secOpts := testutil.ArgValues(args, "--security-opt")
	// Should contain both the built-in opts and the extra one
	assert.Contains(t, secOpts, "writable-cgroups=true")
	assert.Contains(t, secOpts, "label=disable")
	assert.Contains(t, secOpts, "apparmor=unconfined")
}

func TestBuildRunArgs_NoSecurityFieldsByDefault(t *testing.T) {
	cfg := defaultRunConfig()
	args := BuildRunArgs(cfg)

	// No --cap-add, --cap-drop, or --device flags
	caps := testutil.ArgValues(args, "--cap-add")
	assert.Empty(t, caps)

	drops := testutil.ArgValues(args, "--cap-drop")
	assert.Empty(t, drops)

	devs := testutil.ArgValues(args, "--device")
	assert.Empty(t, devs)

	// Only the built-in security opts
	secOpts := testutil.ArgValues(args, "--security-opt")
	assert.Equal(t, []string{"writable-cgroups=true", "label=disable"}, secOpts)
}

// --- Security fields in NodeRunConfigs ---

func TestNodeRunConfigs_SecurityFields(t *testing.T) {
	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: config.RoleController, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g",
				CapAdd: []string{"SYS_ADMIN"}, Devices: []string{"/dev/fuse"}},
			{Role: config.RoleWorker, Count: 2, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g",
				CapAdd: []string{"NET_ADMIN"}, CapDrop: []string{"MKNOD"},
				Devices: []string{"/dev/sda"}, SecurityOpt: []string{"apparmor=unconfined"}},
		},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "172.18.0.2", "25.11.0", "")

	require.Len(t, configs, 3)

	// Controller
	assert.Equal(t, []string{"SYS_ADMIN"}, configs[0].CapAdd)
	assert.Equal(t, []string{"/dev/fuse"}, configs[0].Devices)

	// Workers inherit from their node config
	for _, wc := range configs[1:] {
		assert.Equal(t, []string{"NET_ADMIN"}, wc.CapAdd)
		assert.Equal(t, []string{"MKNOD"}, wc.CapDrop)
		assert.Equal(t, []string{"/dev/sda"}, wc.Devices)
		assert.Equal(t, []string{"apparmor=unconfined"}, wc.SecurityOpt)
	}
}
