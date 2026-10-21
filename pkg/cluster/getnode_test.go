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
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getNodeMock answers GetNode for a running controller of cluster dev whose
// services are all active; partner is the result of inspecting its backup.
func getNodeMock(t *testing.T, partner mock.Result) *mock.Executor {
	t.Helper()
	inspect := `[{"Id":"abc","Name":"/sind-dev-controller","State":{"Status":"running"},"Config":{"Labels":{"sind.role":"controller","sind.cluster":"dev"}},"NetworkSettings":{"Networks":{"sind-dev-net":{"IPAddress":"10.0.0.2"}}}}]`
	return &mock.Executor{OnCall: func(args []string, _ string) mock.Result {
		switch {
		case args[0] == "inspect" && args[1] == "sind-dev-controller-backup":
			return partner
		case args[0] == "inspect":
			return mock.Result{Stdout: inspect}
		case args[2] == "systemctl" && args[3] == "is-active":
			return mock.Result{Stdout: strings.Repeat("active\n", len(args[4:]))}
		case args[2] == "scontrol":
			return mock.Result{Stdout: "Slurmctld(primary) at controller is UP\n"}
		}
		return mock.Result{Err: fmt.Errorf("unexpected: %v", args)}
	}}
}

func TestGetNode(t *testing.T) {
	noBackup := mock.Result{Stderr: "Error: No such object: sind-dev-controller-backup\n", Err: testutil.ExitCode1(t)}
	client := docker.NewClient(getNodeMock(t, noBackup))

	node, err := GetNode(t.Context(), client, mesh.DefaultRealm, "dev", "controller")

	require.NoError(t, err)
	assert.Equal(t, &NodeDetail{
		Container: "sind-dev-controller",
		Cluster:   "dev",
		Role:      config.RoleController,
		Managed:   true,
		FQDN:      "controller.dev.sind.sind",
		IP:        "10.0.0.2",
		Status:    StateRunning,
		Services: ServiceHealth{probe.ServiceMunge: true, probe.ServiceSSHD: true,
			probe.ServiceSlurmctld: true},
	}, node)
}

func TestGetNode_HealthError(t *testing.T) {
	client := docker.NewClient(getNodeMock(t, mock.Result{Stderr: "boom", Err: assert.AnError}))

	node, err := GetNode(t.Context(), client, mesh.DefaultRealm, "dev", "controller")

	require.ErrorContains(t, err, "inspecting controller-backup")
	assert.Nil(t, node)
}

func TestGetNode_NotFound(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error: No such container: sind-dev-ghost\n", testutil.ExitCode1(t))
	m.AddResult(`{"Names":"sind-dev-controller","Labels":"sind.role=controller"}`+"\n", "", nil)

	_, err := GetNode(t.Context(), docker.NewClient(&m), mesh.DefaultRealm, "dev", "ghost")

	require.EqualError(t, err, `node "ghost" not found in cluster "dev"`)
	assert.ErrorIs(t, err, ErrNodeNotFound)
}

// TestGetNode_NotFoundRace checks the fallback when the node exists by the
// time the cluster is listed: it is still reported as not found.
func TestGetNode_NotFoundRace(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error: No such container: sind-dev-ghost\n", testutil.ExitCode1(t))
	m.AddResult(`{"Names":"sind-dev-ghost","Labels":"sind.role=worker"}`+"\n", "", nil)

	_, err := GetNode(t.Context(), docker.NewClient(&m), mesh.DefaultRealm, "dev", "ghost")

	require.EqualError(t, err, `node "ghost" not found in cluster "dev"`)
}

func TestGetNode_ClusterNotFound(t *testing.T) {
	m := &mock.Executor{OnCall: func(args []string, _ string) mock.Result {
		if args[0] == "inspect" {
			return mock.Result{Stderr: "Error: No such container: sind-nope-worker-0\n", Err: testutil.ExitCode1(t)}
		}
		return mock.Result{}
	}}

	_, err := GetNode(t.Context(), docker.NewClient(m), mesh.DefaultRealm, "nope", "worker-0")

	require.EqualError(t, err, `cluster "nope" not found in realm "sind"`)
	assert.ErrorIs(t, err, ErrClusterNotFound)
}

func TestGetNode_InspectError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "permission denied", assert.AnError)

	_, err := GetNode(t.Context(), docker.NewClient(&m), mesh.DefaultRealm, "dev", "worker-0")

	require.ErrorContains(t, err, "inspecting container")
}

func TestGetSSHConfig(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/state")
	t.Setenv("HOME", "/home/u")
	fs := afero.NewMemMapFs()

	conf, err := GetSSHConfig(fs, "ci-42")
	require.NoError(t, err)
	assert.Equal(t, &SSHConfig{Path: "/state/sind/ci-42/ssh_config"}, conf)

	require.NoError(t, afero.WriteFile(fs, conf.Path, []byte("Host *\n"), 0o600))
	conf, err = GetSSHConfig(fs, "ci-42")
	require.NoError(t, err)
	assert.True(t, conf.Exists)
}

func TestGetSSHConfig_NoStateDir(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "")

	_, err := GetSSHConfig(afero.NewMemMapFs(), "ci-42")

	require.Error(t, err)
}

func TestStatus_NodeCounts(t *testing.T) {
	node := func(s State) *NodeStatus { return &NodeStatus{Health: &NodeHealth{State: s}} }
	s := &Status{Nodes: []*NodeStatus{node(StateRunning), node(StateRunning), node(StatePaused), node(StateStopped), node(StateUnknown)}}

	running, stopped, paused := s.NodeCounts()

	assert.Equal(t, 2, running)
	assert.Equal(t, 2, stopped)
	assert.Equal(t, 1, paused)
}
