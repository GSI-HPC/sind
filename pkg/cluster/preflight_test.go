// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/doctor"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- NodeShortNames ---

func TestNodeShortNames(t *testing.T) {
	tests := []struct {
		name  string
		nodes []config.Node
		want  []string
	}{
		{
			name: "minimal: controller + 1 worker",
			nodes: []config.Node{
				{Role: config.RoleController},
				{Role: config.RoleWorker, Count: 1},
			},
			want: []string{"controller", "worker-0"},
		},
		{
			name: "with submitter",
			nodes: []config.Node{
				{Role: config.RoleController},
				{Role: config.RoleSubmitter},
				{Role: config.RoleWorker, Count: 2},
			},
			want: []string{"controller", "submitter", "worker-0", "worker-1"},
		},
		{
			name: "with db",
			nodes: []config.Node{
				{Role: config.RoleController},
				{Role: config.RoleDB},
				{Role: config.RoleWorker},
			},
			want: []string{"controller", "db", "worker-0"},
		},
		{
			name: "with api",
			nodes: []config.Node{
				{Role: config.RoleController},
				{Role: config.RoleAPI},
				{Role: config.RoleWorker},
			},
			want: []string{"controller", "api", "worker-0"},
		},
		{
			name: "with backup controller",
			nodes: []config.Node{
				{Role: config.RoleController, BackupController: true},
				{Role: config.RoleSubmitter},
				{Role: config.RoleWorker, Count: 1},
			},
			want: []string{"controller", "controller-backup", "submitter", "worker-0"},
		},
		{
			name: "multiple worker groups with sequential indexing",
			nodes: []config.Node{
				{Role: config.RoleController},
				{Role: config.RoleWorker, Count: 2},
				{Role: config.RoleWorker, Count: 3},
			},
			want: []string{"controller", "worker-0", "worker-1", "worker-2", "worker-3", "worker-4"},
		},
		{
			name: "unmanaged nodes still get indexed",
			nodes: []config.Node{
				{Role: config.RoleController},
				{Role: config.RoleWorker, Count: 2, Managed: testutil.Ptr(false)},
				{Role: config.RoleWorker, Count: 1},
			},
			want: []string{"controller", "worker-0", "worker-1", "worker-2"},
		},
		{
			name: "worker with default count",
			nodes: []config.Node{
				{Role: config.RoleController},
				{Role: config.RoleWorker},
			},
			want: []string{"controller", "worker-0"},
		},
		{
			name:  "empty nodes",
			nodes: nil,
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NodeShortNames(tt.nodes)
			assert.Equal(t, tt.want, got)
		})
	}
}

// --- PreflightCheck ---

// preflightOnCall returns an OnCall handler for PreflightCheck dispatch:
//   - Network/volume inspects return "exists" when the queried name is a key
//     in existing, and "not found" otherwise.
//   - The filtered `docker ps` call returns NDJSON rows for every name in
//     existingContainers (simulating containers that already exist with the
//     cluster's realm+name labels).
func preflightOnCall(t *testing.T, existing map[string]bool, existingContainers ...string) func([]string, string) mock.Result {
	t.Helper()
	notFound := testutil.ExitCode1(t)
	return func(args []string, _ string) mock.Result {
		if len(args) > 0 && args[0] == "ps" {
			var lines []string
			for _, name := range existingContainers {
				lines = append(lines, fmt.Sprintf(
					`{"ID":"cid","Names":"%s","State":"running","Image":"img","Labels":""}`, name))
			}
			return mock.Result{Stdout: strings.Join(lines, "\n")}
		}
		name := args[len(args)-1]
		if existing[name] {
			return mock.Result{}
		}
		return mock.Result{Stderr: "Error: No such object\n", Err: notFound}
	}
}

func TestPreflightCheck_NoConflicts(t *testing.T) {
	var m mock.Executor
	m.OnCall = preflightOnCall(t, nil)
	c := docker.NewClient(&m)

	cfg := minimalConfig()
	err := PreflightCheck(t.Context(), c, mesh.DefaultRealm, cfg)

	require.NoError(t, err)
	assert.Len(t, m.Calls, 6) // network + 3 volumes + 2 filtered ps
}

func TestPreflightCheck_BackupControllerStateVolume(t *testing.T) {
	var m mock.Executor
	m.OnCall = preflightOnCall(t, map[string]bool{"sind-dev-state": true})
	c := docker.NewClient(&m)

	cfg := minimalConfig()
	cfg.Nodes[0].BackupController = true
	err := PreflightCheck(t.Context(), c, mesh.DefaultRealm, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "volume sind-dev-state")
}

func TestPreflightCheck_ConflictingBackupController(t *testing.T) {
	var m mock.Executor
	m.OnCall = preflightOnCall(t, nil, "sind-dev-controller-backup")
	c := docker.NewClient(&m)

	cfg := minimalConfig()
	cfg.Nodes[0].BackupController = true
	err := PreflightCheck(t.Context(), c, mesh.DefaultRealm, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "container sind-dev-controller-backup")
}

func TestPreflightCheck_ConflictingNetwork(t *testing.T) {
	var m mock.Executor
	m.OnCall = preflightOnCall(t, map[string]bool{"sind-dev-net": true})
	c := docker.NewClient(&m)

	cfg := minimalConfig()
	err := PreflightCheck(t.Context(), c, mesh.DefaultRealm, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "network sind-dev-net")
}

func TestPreflightCheck_ConflictingVolumes(t *testing.T) {
	var m mock.Executor
	m.OnCall = preflightOnCall(t, map[string]bool{
		"sind-dev-config": true,
		"sind-dev-data":   true,
	})
	c := docker.NewClient(&m)

	cfg := minimalConfig()
	err := PreflightCheck(t.Context(), c, mesh.DefaultRealm, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "volume sind-dev-config")
	assert.Contains(t, err.Error(), "volume sind-dev-data")
	assert.NotContains(t, err.Error(), "munge")
}

func TestPreflightCheck_ConflictingContainers(t *testing.T) {
	var m mock.Executor
	m.OnCall = preflightOnCall(t, nil, "sind-dev-controller")
	c := docker.NewClient(&m)

	cfg := minimalConfig()
	err := PreflightCheck(t.Context(), c, mesh.DefaultRealm, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "container sind-dev-controller")
	assert.NotContains(t, err.Error(), "worker")
}

func TestPreflightCheck_MultipleConflicts(t *testing.T) {
	var m mock.Executor
	m.OnCall = preflightOnCall(t,
		map[string]bool{"sind-dev-net": true},
		"sind-dev-controller", "sind-dev-worker-0")
	c := docker.NewClient(&m)

	cfg := minimalConfig()
	err := PreflightCheck(t.Context(), c, mesh.DefaultRealm, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "network sind-dev-net")
	assert.Contains(t, err.Error(), "container sind-dev-controller")
	assert.Contains(t, err.Error(), "container sind-dev-worker-0")
}

func TestPreflightCheck_NetworkCheckError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if len(args) > 0 && args[0] == "ps" {
			return mock.Result{Stdout: ""}
		}
		name := args[len(args)-1]
		if name == "sind-dev-net" {
			return mock.Result{Err: fmt.Errorf("docker daemon not running")}
		}
		return mock.Result{Stderr: "Error: No such object\n", Err: testutil.ExitCode1(t)}
	}
	c := docker.NewClient(&m)

	cfg := minimalConfig()
	err := PreflightCheck(t.Context(), c, mesh.DefaultRealm, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking network")
}

func TestPreflightCheck_VolumeCheckError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if len(args) > 0 && args[0] == "ps" {
			return mock.Result{Stdout: ""}
		}
		name := args[len(args)-1]
		if name == "sind-dev-config" {
			return mock.Result{Err: fmt.Errorf("permission denied")}
		}
		return mock.Result{Stderr: "Error: No such object\n", Err: testutil.ExitCode1(t)}
	}
	c := docker.NewClient(&m)

	cfg := minimalConfig()
	err := PreflightCheck(t.Context(), c, mesh.DefaultRealm, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking volume")
}

func TestPreflightCheck_ContainerCheckError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if len(args) > 0 && args[0] == "ps" && slices.Contains(args, "label=sind.cluster=dev") {
			return mock.Result{Err: fmt.Errorf("connection refused")}
		}
		if len(args) > 0 && args[0] == "ps" {
			return mock.Result{}
		}
		return mock.Result{Stderr: "Error: No such object\n", Err: testutil.ExitCode1(t)}
	}
	c := docker.NewClient(&m)

	cfg := minimalConfig()
	err := PreflightCheck(t.Context(), c, mesh.DefaultRealm, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing cluster containers")
}

// --- checkBridgePorts ---

// onNetworks returns docker ps output for containers connected to the
// given networks: the named ones, then count-len(names) nodes.
func onNetworks(networks string, count int, names ...string) []testutil.PsEntry {
	var entries []testutil.PsEntry
	for i := range count {
		name := fmt.Sprintf("sind-c%d-worker-0", i)
		if i < len(names) {
			name = names[i]
		}
		entries = append(entries, testutil.PsEntry{ID: name, Names: name, State: "exited", Image: "img", Networks: networks})
	}
	return entries
}

func TestCheckBridgePorts(t *testing.T) {
	const relayBoth = "sind-dev-net,sind-mesh"
	tests := []struct {
		name    string
		entries []testutil.PsEntry
		nodes   int
		wantErr string
	}{
		{"new realm", nil, 2, ""},
		// The DNS container and the relay join a new mesh too.
		{"new realm at the limit", nil, docker.MaxBridgeEndpoints - 2, ""},
		{"new realm over the limit", nil, docker.MaxBridgeEndpoints - 1,
			"network sind-mesh has 0 containers and would get 1024 more, but a Docker bridge network holds at most 1023"},
		{"mesh at the limit", onNetworks("sind-mesh", 1013, "sind-dns", "sind-ssh"), 10, ""},
		{"mesh over the limit", onNetworks("sind-mesh", 1014, "sind-dns", "sind-ssh"), 10,
			"network sind-mesh has 1014 containers and would get 10 more, but a Docker bridge network holds at most 1023 (a Linux bridge has 1,024 ports): " +
				"the realm's mesh holds the nodes of all its clusters, while another realm (--realm) has a mesh of its own"},
		{"cluster network over the limit", append(
			append(onNetworks("sind-mesh", 1, "sind-dns"), onNetworks(relayBoth, 1, "sind-ssh")...),
			onNetworks("sind-dev-net", 1021)...), 2,
			"network sind-dev-net has 1022 containers and would get 2 more, but a Docker bridge network holds at most 1023 (a Linux bridge has 1,024 ports): " +
				"the cluster network holds the cluster's nodes and the SSH relay"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			stdout := ""
			if len(tt.entries) > 0 {
				stdout = testutil.NDJSON(tt.entries...)
			}
			m.AddResult(stdout, "", nil)

			err := checkBridgePorts(t.Context(), docker.NewClient(&m), mesh.DefaultRealm, "dev", tt.nodes)

			require.Len(t, m.Calls, 1)
			assert.Equal(t, []string{"ps", "-a", "--no-trunc", "--format", "json",
				"--filter", "network=sind-mesh", "--filter", "network=sind-dev-net"}, m.Calls[0].Args)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, ErrNetworkFull)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestCheckBridgePorts_ListError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("connection refused"))

	err := checkBridgePorts(t.Context(), docker.NewClient(&m), mesh.DefaultRealm, "dev", 1)

	require.EqualError(t, err, "listing the containers of sind-mesh and sind-dev-net: connection refused")
}

func TestPreflightCheck_MeshFull(t *testing.T) {
	full := testutil.NDJSON(onNetworks("sind-mesh", docker.MaxBridgeEndpoints, "sind-dns", "sind-ssh")...)
	var m mock.Executor
	base := preflightOnCall(t, nil)
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "ps" && slices.Contains(args, "network=sind-mesh") {
			return mock.Result{Stdout: full}
		}
		return base(args, stdin)
	}

	err := PreflightCheck(t.Context(), docker.NewClient(&m), mesh.DefaultRealm, minimalConfig())

	require.ErrorIs(t, err, ErrNetworkFull)
	assert.Contains(t, err.Error(), "network sind-mesh has 1023 containers and would get 2 more")
}

func TestPreflightCheck_MultiCompute(t *testing.T) {
	var m mock.Executor
	m.OnCall = preflightOnCall(t, nil, "sind-dev-worker-0", "sind-dev-worker-2")
	c := docker.NewClient(&m)

	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: config.RoleController},
			{Role: config.RoleWorker, Count: 3},
		},
	}
	err := PreflightCheck(t.Context(), c, mesh.DefaultRealm, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "container sind-dev-worker-0")
	assert.Contains(t, err.Error(), "container sind-dev-worker-2")
	assert.NotContains(t, err.Error(), "worker-1")
}

// --- CheckDaemon ---

func TestCheckDaemon(t *testing.T) {
	tests := []struct {
		name    string
		info    string
		wantErr error
	}{
		{"rootful", `{"ServerVersion":"29.1.0","CgroupVersion":"2","SecurityOptions":["name=seccomp,profile=builtin","name=cgroupns"]}`, nil},
		{"rootless", `{"ServerVersion":"29.1.0","CgroupVersion":"2","SecurityOptions":["name=seccomp,profile=builtin","name=rootless","name=cgroupns"]}`, ErrRootlessDaemon},
		{"userns-remap", `{"ServerVersion":"29.1.0","CgroupVersion":"2","SecurityOptions":["name=seccomp,profile=builtin","name=userns","name=cgroupns"]}`, ErrUsernsRemap},
		{"cgroup v1", `{"ServerVersion":"29.1.0","CgroupVersion":"1","SecurityOptions":["name=seccomp,profile=builtin"]}`, ErrCgroupV1},
		{"no cgroup version", `{"ServerVersion":"29.1.0","SecurityOptions":[]}`, nil},
		{"Docker 28", `{"ServerVersion":"28.0.0","CgroupVersion":"2","SecurityOptions":[]}`, nil},
		{"Docker 27", `{"ServerVersion":"27.5.1","CgroupVersion":"2","SecurityOptions":["name=seccomp,profile=builtin"]}`, ErrDockerTooOld},
		{"unparsable version", `{"ServerVersion":"dev","CgroupVersion":"2","SecurityOptions":[]}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			m.AddResult(tt.info, "", nil)
			err := CheckDaemon(t.Context(), docker.NewClient(&m))
			if tt.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.wantErr)
			if tt.wantErr == ErrCgroupV1 {
				assert.Contains(t, err.Error(), "sind requires cgroup v2")
				return
			}
			if tt.wantErr == ErrDockerTooOld {
				assert.EqualError(t, err, "the Docker Engine is too old: 27.5.1; sind requires Docker Engine 28.0 or later, for the writable cgroups and network options of its nodes")
				return
			}
			assert.Contains(t, err.Error(), "sind needs a rootful Docker daemon without userns-remap")
		})
	}
}

func TestCheckDaemon_InfoError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Cannot connect to the Docker daemon\n", testutil.ExitCode1(t))
	err := CheckDaemon(t.Context(), docker.NewClient(&m))
	require.ErrorContains(t, err, "querying the Docker daemon: exit status 1: Cannot connect to the Docker daemon")
}

// --- CheckNsdelegate ---

// probeMounts is the mount table that CheckNsdelegate's probe container
// shows on a cgroup v2 host that mounts cgroup2 with nsdelegate.
const probeMounts = "overlay / overlay rw,relatime,lowerdir=/l,upperdir=/u,workdir=/w 0 0\n" +
	"cgroup /sys/fs/cgroup cgroup2 ro,nosuid,nodev,noexec,relatime,nsdelegate,memory_recursiveprot 0 0\n"

// isNsdelegateProbe reports whether args run CheckNsdelegate's probe
// container.
func isNsdelegateProbe(args []string) bool {
	return args[0] == "run" && slices.Contains(args, "/proc/self/mounts")
}

// TestCheckNsdelegateLifecycle probes a real daemon in integration mode:
// the CI runners mount cgroup2 with nsdelegate.
func TestCheckNsdelegateLifecycle(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	image := os.Getenv("SIND_TEST_IMAGE")
	if image == "" {
		image = "ghcr.io/gsi-hpc/sind-node:latest"
	}
	if !rec.IsIntegration() {
		rec.AddResult(probeMounts, "", nil)
	}

	require.NoError(t, CheckNsdelegate(t.Context(), c, image))

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestCheckNsdelegate(t *testing.T) {
	var m mock.Executor
	m.AddResult(probeMounts, "", nil)

	require.NoError(t, CheckNsdelegate(t.Context(), docker.NewClient(&m), "img:1"))
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"run", "--rm", "--network", "none", "--entrypoint", "cat", "img:1", "/proc/self/mounts"}, m.Calls[0].Args)
}

func TestCheckNsdelegate_Missing(t *testing.T) {
	var m mock.Executor
	m.AddResult("cgroup /sys/fs/cgroup cgroup2 ro,nosuid,nodev,noexec,relatime 0 0\n", "", nil)

	err := CheckNsdelegate(t.Context(), docker.NewClient(&m), "img:1")
	require.ErrorIs(t, err, ErrNoNsdelegate)
	assert.Equal(t, "the Docker host mounts cgroup2 without nsdelegate, which sind requires\n\n"+
		doctor.NsdelegateRemediation("/sys/fs/cgroup"), err.Error())
}

func TestCheckNsdelegate_NoCgroup2(t *testing.T) {
	var m mock.Executor
	m.AddResult("tmpfs /sys/fs/cgroup tmpfs ro,nosuid,nodev,noexec,mode=755 0 0\n"+
		"cgroup /sys/fs/cgroup/memory cgroup ro,nosuid,nodev,noexec,relatime,memory 0 0\n", "", nil)

	err := CheckNsdelegate(t.Context(), docker.NewClient(&m), "img:1")
	require.ErrorIs(t, err, ErrCgroupV1)
	assert.Contains(t, err.Error(), "a container of img:1 has no cgroup2 at /sys/fs/cgroup")
}

func TestCheckNsdelegate_ProbeError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "docker: Error response from daemon: pull access denied for img\n", fmt.Errorf("exit status 125"))

	err := CheckNsdelegate(t.Context(), docker.NewClient(&m), "img:1")
	require.ErrorContains(t, err, "checking the Docker host for nsdelegate: reading the mount table in a container of img:1: exit status 125")
	assert.NotErrorIs(t, err, ErrNoNsdelegate)
}

// --- helpers ---

func minimalConfig() *config.Cluster {
	return &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: config.RoleController},
			{Role: config.RoleWorker, Count: 1},
		},
	}
}
