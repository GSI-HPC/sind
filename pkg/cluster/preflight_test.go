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
	assert.Len(t, m.Calls, 5) // network + 3 volumes + 1 filtered ps
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
		if len(args) > 0 && args[0] == "ps" {
			return mock.Result{Err: fmt.Errorf("connection refused")}
		}
		return mock.Result{Stderr: "Error: No such object\n", Err: testutil.ExitCode1(t)}
	}
	c := docker.NewClient(&m)

	cfg := minimalConfig()
	err := PreflightCheck(t.Context(), c, mesh.DefaultRealm, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing cluster containers")
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

// --- helpers ---

// addNotFound adds n "not found" results (exit code 1) to the mock.
func addNotFound(t *testing.T, m *mock.Executor, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		m.AddResult("", "Error: No such object\n",
			testutil.ExitCode1(t))
	}
}

func minimalConfig() *config.Cluster {
	return &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: config.RoleController},
			{Role: config.RoleWorker, Count: 1},
		},
	}
}
