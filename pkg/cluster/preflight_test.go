// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
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
				{Role: "controller"},
				{Role: "worker", Count: 1},
			},
			want: []string{"controller", "worker-0"},
		},
		{
			name: "with submitter",
			nodes: []config.Node{
				{Role: "controller"},
				{Role: "submitter"},
				{Role: "worker", Count: 2},
			},
			want: []string{"controller", "submitter", "worker-0", "worker-1"},
		},
		{
			name: "multiple worker groups with sequential indexing",
			nodes: []config.Node{
				{Role: "controller"},
				{Role: "worker", Count: 2},
				{Role: "worker", Count: 3},
			},
			want: []string{"controller", "worker-0", "worker-1", "worker-2", "worker-3", "worker-4"},
		},
		{
			name: "unmanaged nodes still get indexed",
			nodes: []config.Node{
				{Role: "controller"},
				{Role: "worker", Count: 2, Managed: boolPtr(false)},
				{Role: "worker", Count: 1},
			},
			want: []string{"controller", "worker-0", "worker-1", "worker-2"},
		},
		{
			name: "worker with default count",
			nodes: []config.Node{
				{Role: "controller"},
				{Role: "worker"},
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

func TestPreflightCheck_NoConflicts(t *testing.T) {
	m := preflightMock(t, 6, false) // network + 3 volumes + 2 containers
	c := docker.NewClient(m)

	cfg := minimalConfig()
	err := PreflightCheck(t.Context(), c, cfg)

	require.NoError(t, err)
	assert.Len(t, m.Calls, 6)
}

func TestPreflightCheck_ConflictingNetwork(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil) // network exists
	addNotFound(t, &m, 5)    // volumes + containers
	c := docker.NewClient(&m)

	cfg := minimalConfig()
	err := PreflightCheck(t.Context(), c, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "network sind-dev-net")
}

func TestPreflightCheck_ConflictingVolumes(t *testing.T) {
	var m docker.MockExecutor
	addNotFound(t, &m, 1)    // network
	m.AddResult("", "", nil) // config volume exists
	addNotFound(t, &m, 1)    // munge volume
	m.AddResult("", "", nil) // data volume exists
	addNotFound(t, &m, 2)    // containers
	c := docker.NewClient(&m)

	cfg := minimalConfig()
	err := PreflightCheck(t.Context(), c, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "volume sind-dev-config")
	assert.Contains(t, err.Error(), "volume sind-dev-data")
	assert.NotContains(t, err.Error(), "munge")
}

func TestPreflightCheck_ConflictingContainers(t *testing.T) {
	var m docker.MockExecutor
	addNotFound(t, &m, 4)    // network + 3 volumes
	m.AddResult("", "", nil) // controller exists
	addNotFound(t, &m, 1)    // worker-0
	c := docker.NewClient(&m)

	cfg := minimalConfig()
	err := PreflightCheck(t.Context(), c, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "container sind-dev-controller")
	assert.NotContains(t, err.Error(), "worker")
}

func TestPreflightCheck_MultipleConflicts(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil) // network exists
	addNotFound(t, &m, 3)    // volumes
	m.AddResult("", "", nil) // controller exists
	m.AddResult("", "", nil) // worker-0 exists
	c := docker.NewClient(&m)

	cfg := minimalConfig()
	err := PreflightCheck(t.Context(), c, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "network sind-dev-net")
	assert.Contains(t, err.Error(), "container sind-dev-controller")
	assert.Contains(t, err.Error(), "container sind-dev-worker-0")
}

func TestPreflightCheck_NetworkCheckError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("docker daemon not running"))
	c := docker.NewClient(&m)

	cfg := minimalConfig()
	err := PreflightCheck(t.Context(), c, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking network")
}

func TestPreflightCheck_VolumeCheckError(t *testing.T) {
	var m docker.MockExecutor
	addNotFound(t, &m, 1) // network
	m.AddResult("", "", fmt.Errorf("permission denied"))
	c := docker.NewClient(&m)

	cfg := minimalConfig()
	err := PreflightCheck(t.Context(), c, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking volume")
}

func TestPreflightCheck_ContainerCheckError(t *testing.T) {
	var m docker.MockExecutor
	addNotFound(t, &m, 4) // network + volumes
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)

	cfg := minimalConfig()
	err := PreflightCheck(t.Context(), c, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking container")
}

func TestPreflightCheck_MultiCompute(t *testing.T) {
	// 1 controller + 3 worker = 4 containers + 1 network + 3 volumes = 8 checks
	var m docker.MockExecutor
	addNotFound(t, &m, 4)    // network + volumes
	addNotFound(t, &m, 1)    // controller
	m.AddResult("", "", nil) // worker-0 exists
	addNotFound(t, &m, 1)    // worker-1
	m.AddResult("", "", nil) // worker-2 exists
	c := docker.NewClient(&m)

	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: "controller"},
			{Role: "worker", Count: 3},
		},
	}
	err := PreflightCheck(t.Context(), c, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "container sind-dev-worker-0")
	assert.Contains(t, err.Error(), "container sind-dev-worker-2")
	assert.NotContains(t, err.Error(), "worker-1")
}

// --- helpers ---

func boolPtr(b bool) *bool { return &b }

func minimalConfig() *config.Cluster {
	return &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: "controller"},
			{Role: "worker", Count: 1},
		},
	}
}

// preflightMock returns a MockExecutor with n "not found" results (exit code 1).
// If existAll is true, all results return success instead.
func preflightMock(t *testing.T, n int, existAll bool) *docker.MockExecutor {
	t.Helper()
	var m docker.MockExecutor
	for i := 0; i < n; i++ {
		if existAll {
			m.AddResult("", "", nil)
		} else {
			addNotFound(t, &m, 1)
		}
	}
	return &m
}

// addNotFound adds n "not found" results (exit code 1) to the mock.
func addNotFound(t *testing.T, m *docker.MockExecutor, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		m.AddResult("", "Error: No such object\n",
			&exec.ExitError{ProcessState: exitCode1(t)})
	}
}

// exitCode1 runs a command that exits with code 1 and returns its ProcessState.
func exitCode1(t *testing.T) *os.ProcessState {
	t.Helper()
	cmd := exec.Command("sh", "-c", "exit 1")
	err := cmd.Run()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	return exitErr.ProcessState
}
