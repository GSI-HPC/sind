// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"errors"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slurmStatus returns the Status of cluster dev with a controller pair and
// two workers, worker-1 unmanaged; primary tells whether the primary
// controller runs.
func slurmStatus(primary bool) *Status {
	state := StateRunning
	if !primary {
		state = StateStopped
	}
	node := func(name string, role config.Role, managed bool, state State) *NodeStatus {
		return &NodeStatus{Name: name, Role: role, Managed: managed, Health: &NodeHealth{State: state}}
	}
	return &Status{Realm: "sind", Name: "dev", Nodes: []*NodeStatus{
		node("controller-backup.dev", config.RoleController, true, StateRunning),
		node("controller.dev", config.RoleController, true, state),
		node("worker-0.dev", config.RoleWorker, true, StateRunning),
		node("worker-1.dev", config.RoleWorker, false, StateRunning),
		node("worker-2.dev", config.RoleWorker, true, StateStopped),
	}}
}

func TestAddSlurmStates(t *testing.T) {
	for _, tc := range []struct {
		name       string
		realm      string
		primary    bool
		controller string
	}{
		{"primary runs", "sind", true, "sind-dev-controller"},
		{"backup in control", "sind", false, "sind-dev-controller-backup"},
		{"realm of the status", "ci", true, "ci-dev-controller"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m mock.Executor
			m.AddResult("worker-0|drained|0/0/2/2|2048|all*|maintenance\nworker-2|down*|0/0/2/2|2048|all*|Not responding\n", "", nil)
			status := slurmStatus(tc.primary)
			status.Realm = tc.realm

			AddSlurmStates(t.Context(), docker.NewClient(&m), status)

			require.Len(t, m.Calls, 1)
			assert.Equal(t, []string{"exec", tc.controller, "sinfo", "--noheader", "--Node", "--format=%N|%T|%C|%m|%P|%E", "--nodes=worker-0,worker-2"}, m.Calls[0].Args)
			assert.Nil(t, status.Nodes[0].Health.Slurm, "controllers have none")
			assert.Equal(t, &SlurmNodeState{State: "drained", Reason: "maintenance", Registered: true}, status.Nodes[2].Health.Slurm)
			assert.Nil(t, status.Nodes[3].Health.Slurm, "unmanaged workers have none")
			assert.Equal(t, &SlurmNodeState{State: "down*", Reason: "Not responding"}, status.Nodes[4].Health.Slurm)
		})
	}
}

func TestAddSlurmStates_NothingToRead(t *testing.T) {
	var m mock.Executor
	client := docker.NewClient(&m)

	noWorkers := slurmStatus(true)
	noWorkers.Nodes = noWorkers.Nodes[:2]
	AddSlurmStates(t.Context(), client, noWorkers)

	noController := slurmStatus(true)
	noController.Nodes = noController.Nodes[2:]
	AddSlurmStates(t.Context(), client, noController)

	assert.Empty(t, m.Calls, "no sinfo without a managed worker or a running controller")
}

func TestAddSlurmStates_SinfoFails(t *testing.T) {
	for _, out := range []mock.Result{{Err: errors.New("exit status 1")}, {Stdout: "worker-0|idle\n"}} {
		var m mock.Executor
		m.AddResult(out.Stdout, "", out.Err)
		status := slurmStatus(true)

		AddSlurmStates(t.Context(), docker.NewClient(&m), status)

		for _, n := range status.Nodes {
			assert.Nil(t, n.Health.Slurm, n.Name)
		}
	}
}

func TestSlurmNodeStates_TimesOut(t *testing.T) {
	saved := slurmInfoTimeout
	slurmInfoTimeout = 20 * time.Millisecond
	t.Cleanup(func() { slurmInfoTimeout = saved })

	_, err := slurmNodeStates(t.Context(), docker.NewClient(hangingSinfo{&mock.Executor{}}), "sind-dev-controller", []string{"worker-0"})
	require.EqualError(t, err, "sinfo on the controller did not answer within 20ms")
}

// slurmStateContainers lists cluster dev: a controller in state, and
// worker-0.
func slurmStateContainers(state string) mock.Result {
	return mock.Result{Stdout: testutil.NDJSON(
		testutil.PsEntry{ID: "c1", Names: "sind-dev-controller", State: state, Image: "img:1", Labels: "sind.cluster=dev,sind.role=controller"},
		testutil.PsEntry{ID: "c2", Names: "sind-dev-worker-0", State: "running", Image: "img:1", Labels: "sind.cluster=dev,sind.role=worker"},
	)}
}

func TestNodeSlurmState(t *testing.T) {
	var m mock.Executor
	ps := slurmStateContainers("running")
	m.AddResult(ps.Stdout, "", nil)
	m.AddResult("worker-0|idle|0/2/0/2|2048|all*|none\n", "", nil)

	state := nodeSlurmState(t.Context(), docker.NewClient(&m), "sind", "dev", "worker-0")

	assert.Equal(t, &SlurmNodeState{State: "idle", Registered: true}, state)
	require.Len(t, m.Calls, 2)
	assert.Equal(t, "--nodes=worker-0", m.Calls[1].Args[len(m.Calls[1].Args)-1])
}

func TestNodeSlurmState_Unknown(t *testing.T) {
	for name, results := range map[string][]mock.Result{
		"ps fails":              {{Err: errors.New("daemon down")}},
		"controller stopped":    {slurmStateContainers("exited")},
		"no controller":         {{Stdout: ""}},
		"sinfo fails":           {slurmStateContainers("running"), {Err: errors.New("exit status 1")}},
		"worker not in a sinfo": {slurmStateContainers("running"), {Stdout: ""}},
	} {
		t.Run(name, func(t *testing.T) {
			var m mock.Executor
			for _, r := range results {
				m.AddResult(r.Stdout, r.Stderr, r.Err)
			}
			assert.Nil(t, nodeSlurmState(t.Context(), docker.NewClient(&m), "sind", "dev", "worker-0"))
		})
	}
}
