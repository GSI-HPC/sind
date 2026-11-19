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
		State:     StateRunning,
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

// TestGetNode_OtherRealm checks that a container of another realm whose
// name joins to the node's, ci-42/dev for ci/42-dev, is not the node.
func TestGetNode_OtherRealm(t *testing.T) {
	m := &mock.Executor{OnCall: func(args []string, _ string) mock.Result {
		if args[0] == "inspect" {
			return mock.Result{Stdout: `[{"Id":"abc","Name":"/ci-42-dev-controller","State":{"Status":"running"},"Config":{"Labels":{"sind.realm":"ci-42","sind.cluster":"dev","sind.role":"controller"}}}]`}
		}
		return mock.Result{}
	}}

	_, err := GetNode(t.Context(), docker.NewClient(m), "ci", "42-dev", "controller")

	require.EqualError(t, err, `cluster "42-dev" not found in realm "ci"`)
	assert.ErrorIs(t, err, ErrClusterNotFound)
}

// TestGetNode_Helper checks that a helper container left behind, which has
// the realm and cluster labels but no role, is not a node.
func TestGetNode_Helper(t *testing.T) {
	m := &mock.Executor{OnCall: func(args []string, _ string) mock.Result {
		switch args[0] {
		case "inspect":
			return mock.Result{Stdout: `[{"Id":"abc","Name":"/sind-dev-config-helper","State":{"Status":"created"},"Config":{"Labels":{"sind.realm":"sind","sind.cluster":"dev"}}}]`}
		case "ps":
			return mock.Result{Stdout: `{"Names":"sind-dev-config-helper","Labels":"sind.realm=sind,sind.cluster=dev"}` + "\n"}
		}
		return mock.Result{}
	}}

	node, err := GetNode(t.Context(), docker.NewClient(m), mesh.DefaultRealm, "dev", "config-helper")

	require.EqualError(t, err, `node "config-helper" not found in cluster "dev"`)
	assert.ErrorIs(t, err, ErrNodeNotFound)
	assert.Nil(t, node)
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

	require.ErrorContains(t, err, "$HOME is not defined")
}

func TestStatus_NodeCounts(t *testing.T) {
	node := func(s State) *NodeStatus { return &NodeStatus{Health: &NodeHealth{State: s}} }
	s := &Status{Nodes: []*NodeStatus{node(StateRunning), node(StateRunning), node(StatePaused), node(StateStopped), node(StateUnknown)}}

	running, stopped, paused := s.NodeCounts()

	assert.Equal(t, 2, running)
	assert.Equal(t, 2, stopped)
	assert.Equal(t, 1, paused)
}

// GetNode of a managed worker adds what slurmctld says about it.
func TestGetNode_Worker(t *testing.T) {
	inspect := `[{"Id":"abc","Name":"/sind-dev-worker-0","State":{"Status":"running"},"Config":{"Labels":{"sind.role":"worker","sind.cluster":"dev"}},"NetworkSettings":{"Networks":{"sind-dev-net":{"IPAddress":"10.0.0.3"}}}}]`
	client := docker.NewClient(&mock.Executor{OnCall: func(args []string, _ string) mock.Result {
		switch {
		case args[0] == "inspect":
			return mock.Result{Stdout: inspect}
		case args[0] == "ps":
			return slurmStateContainers("running")
		case args[2] == "systemctl" && args[3] == "is-active":
			return mock.Result{Stdout: strings.Repeat("active\n", len(args[4:]))}
		case args[1] == "sind-dev-controller" && args[2] == "sinfo":
			return mock.Result{Stdout: "worker-0|allocated|2/0/0/2|2048|all*|none\n"}
		}
		return mock.Result{Err: fmt.Errorf("unexpected: %v", args)}
	}})

	node, err := GetNode(t.Context(), client, mesh.DefaultRealm, "dev", "worker-0")

	require.NoError(t, err)
	assert.Equal(t, &SlurmNodeState{State: "allocated", Registered: true}, node.Slurm)
}
