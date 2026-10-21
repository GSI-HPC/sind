// SPDX-License-Identifier: LGPL-3.0-or-later

package demo

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/internal/ui/run"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
)

// newSim returns a simulator on the seeded world that runs a thousand times
// faster than real time.
func newSim(t *testing.T) (*Sim, *store.Store) {
	t.Helper()
	w, st := seededStore(t)
	s := NewSim(st, w, liveClock{speed: 1000})
	s.seq = 100
	t.Cleanup(func() { s.Shutdown(10 * time.Second) })
	return s, st
}

// start starts the command and waits for its task to end.
func (s *Sim) startWait(t *testing.T, st *store.Store, argv ...string) store.Task {
	t.Helper()
	id, err := s.Start(run.Spec{Action: "test", Argv: append([]string{"sind"}, argv...), Display: strings.Join(argv, " ")})
	require.NoError(t, err)
	return waitTask(t, st, id)
}

func waitTask(t *testing.T, st *store.Store, id string) store.Task {
	t.Helper()
	var task store.Task
	require.Eventually(t, func() bool {
		var ok bool
		task, ok = st.Snapshot().Task(id)
		return ok && task.Done()
	}, 10*time.Second, 5*time.Millisecond)
	return task
}

func nodeStates(st *store.Store, realm, clusterName string) map[string]cluster.State {
	out := map[string]cluster.State{}
	for _, n := range st.Snapshot().Nodes[realm] {
		if n.Cluster == clusterName {
			short, _, _ := strings.Cut(n.FQDN, ".")
			out[short] = n.State
		}
	}
	return out
}

const nightly = `kind: Cluster
name: nightly
realm: ci
defaults:
  image: ghcr.io/gsi-hpc/sind-node:26.05.4
users:
  - alice
monitoring: true
nodes:
  - role: controller
    backupController: true
  - worker: 2
`

func TestSim_CreateCluster(t *testing.T) {
	s, st := newSim(t)
	require.NoError(t, s.SaveFile(ConfigDir+"/nightly.yaml", []byte(nightly)))

	id, err := s.Start(run.Spec{Action: "create-cluster", Realm: "ci", Cluster: "nightly", Display: "sind create cluster",
		Argv: []string{"sind", "create", "cluster", "--config", ConfigDir + "/nightly.yaml", "--data", "volume", "--pull"}})
	require.NoError(t, err)
	task, ok := st.Snapshot().Task(id)
	require.True(t, ok)
	assert.Equal(t, store.TaskRunning, task.Status)
	assert.Equal(t, 1, s.Running())

	task = waitTask(t, st, id)
	assert.Equal(t, store.TaskOK, task.Status)
	assert.Zero(t, task.Exit)

	sn := st.Snapshot()
	r, ok := sn.RunOfTrace(task.Trace)
	require.True(t, ok, "the task finds its run by the trace")
	assert.Equal(t, store.StatusOK, r.Status)
	assert.Equal(t, "create cluster nightly", r.Title, "sind names the cluster in the command span")
	assert.Equal(t, []string{"pull images", "preflight", "nodes", "mesh registration", "slurm"}, stepsOf(r))
	assert.Equal(t, map[string]cluster.State{
		"controller": cluster.StateRunning, "controller-backup": cluster.StateRunning,
		"worker-0": cluster.StateRunning, "worker-1": cluster.StateRunning,
	}, nodeStates(st, "ci", "nightly"))
	assert.Equal(t, 2, sn.Realms[0].Clusters, "in the realm the config names")
	assert.NotNil(t, sn.MonitoringOf("ci"), "the config asked for monitoring")
	assert.Nil(t, sn.Locks["ci"], "the lock is free again")
	var volumes []string
	for _, v := range sn.Volumes["ci"] {
		volumes = append(volumes, v.Name)
	}
	assert.Contains(t, volumes, "ci-nightly-data", "--data volume")
	assert.Equal(t, 0, s.Running())
}

func TestSim_CreateCluster_HostPathAndOldSlurm(t *testing.T) {
	s, st := newSim(t)
	cfg := "kind: Cluster\nname: hp\ndefaults:\n  image: ghcr.io/gsi-hpc/sind-node:25.11.8\nnodes:\n  - controller\n  - worker\n"
	require.NoError(t, s.SaveFile("/c/hp.yaml", []byte(cfg)))
	task := s.startWait(t, st, "create", "cluster", "--config", "/c/hp.yaml", "--data", "/scratch/hp")
	assert.Equal(t, store.TaskOK, task.Status)
	sn := st.Snapshot()
	c, ok := sn.Cluster("sind", "hp")
	require.True(t, ok)
	assert.Equal(t, "25.11.8", c.SlurmVersion)
	assert.Equal(t, 2, c.NodeCount, "a worker group without a count is one worker")
	assert.Equal(t, "hostPath", string(sn.Status("sind", "hp").Mounts[2].Type))
	assert.Equal(t, "/scratch/hp", sn.Status("sind", "hp").Mounts[2].Source)
}

func TestSim_CreateCluster_PullFails(t *testing.T) {
	s, st := newSim(t)
	cfg := "kind: Cluster\nname: bad\ndefaults:\n  image: example.org/none:broken\nnodes:\n  - controller\n"
	require.NoError(t, s.SaveFile("/c/bad.yaml", []byte(cfg)))
	task := s.startWait(t, st, "create", "cluster", "--config", "/c/bad.yaml", "--data", "volume")
	assert.Equal(t, store.TaskFailed, task.Status)
	assert.Equal(t, 1, task.Exit)
	assert.Contains(t, task.Err, "pull access denied for example.org/none")
	assert.Contains(t, task.Output, "Error: pulling")
	_, ok := st.Snapshot().Cluster("sind", "bad")
	assert.False(t, ok)
	r, _ := st.Snapshot().RunOfTrace(task.Trace)
	assert.Equal(t, store.StatusFailed, r.Status)
}

func TestSim_CreateCluster_Errors(t *testing.T) {
	s, _ := newSim(t)
	_, err := s.Start(run.Spec{Argv: []string{"sind", "create", "cluster", "--config", "/nowhere.yaml"}})
	assert.ErrorContains(t, err, "not saved")
	require.NoError(t, s.SaveFile("/bad.yaml", []byte("kind: [")))
	_, err = s.Start(run.Spec{Argv: []string{"sind", "create", "cluster", "--config", "/bad.yaml"}})
	assert.ErrorContains(t, err, "/bad.yaml")
}

func TestSim_StartRefuses(t *testing.T) {
	s, _ := newSim(t)
	for _, argv := range [][]string{nil, {"ls"}} {
		_, err := s.Start(run.Spec{Argv: argv})
		assert.EqualError(t, err, "not a sind command")
	}
	s.frozen = true
	_, err := s.Start(run.Spec{Argv: []string{"sind", "power", "on"}})
	assert.EqualError(t, err, "the still demo runs no commands")
}

func TestSim_CreateWorker(t *testing.T) {
	s, st := newSim(t)
	task := s.startWait(t, st, "create", "worker", "dev", "--count", "2", "--cpus", "2")
	assert.Equal(t, store.TaskOK, task.Status)
	states := nodeStates(st, "sind", "dev")
	assert.Equal(t, cluster.StateRunning, states["worker-6"])
	assert.Equal(t, cluster.StateRunning, states["worker-7"])
	assert.Len(t, states, 13)
	r, _ := st.Snapshot().RunOfTrace(task.Trace)
	assert.Equal(t, []string{"preflight", "nodes", "slurm"}, stepsOf(r))

	task = s.startWait(t, st, "create", "worker") // the default cluster
	assert.Equal(t, store.TaskOK, task.Status)
	assert.Contains(t, nodeStates(st, "sind", "default"), "worker-1")
}

func TestSim_DeleteCluster(t *testing.T) {
	s, st := newSim(t)
	task := s.startWait(t, st, "delete", "cluster", "legacy")
	assert.Equal(t, store.TaskOK, task.Status)
	_, ok := st.Snapshot().Cluster("sind", "legacy")
	assert.False(t, ok)
	r, _ := st.Snapshot().RunOfTrace(task.Trace)
	assert.Equal(t, []string{"mesh deregistration", "nodes", "network", "volumes"}, stepsOf(r), "not the last: the mesh stays")

	task = s.startWait(t, st, "--realm", "ci", "delete", "cluster", "pr-145")
	r, _ = st.Snapshot().RunOfTrace(task.Trace)
	assert.Equal(t, []string{"mesh deregistration", "nodes", "network", "volumes", "mesh"}, stepsOf(r), "the last of its realm")
	require.Len(t, st.Snapshot().Realms, 1, "the realm is gone with its last cluster")
	assert.Equal(t, "sind", st.Snapshot().Realms[0].Name)

	s.startWait(t, st, "delete", "cluster") // the default cluster
	_, ok = st.Snapshot().Cluster("sind", "default")
	assert.False(t, ok)
}

func TestSim_DeleteRealm(t *testing.T) {
	s, st := newSim(t)
	task := s.startWait(t, st, "--realm", "sind", "delete", "realm")
	assert.Equal(t, store.TaskOK, task.Status)
	require.Len(t, st.Snapshot().Realms, 1)
	assert.Equal(t, "ci", st.Snapshot().Realms[0].Name)
}

func TestSim_Nodes(t *testing.T) {
	s, st := newSim(t)
	s.startWait(t, st, "delete", "worker", "worker-[1-2].dev")
	states := nodeStates(st, "sind", "dev")
	assert.NotContains(t, states, "worker-1")
	assert.NotContains(t, states, "worker-2")
	assert.Contains(t, states, "worker-3")

	s.startWait(t, st, "power", "shutdown", "worker-3.dev", "controller")
	assert.Equal(t, cluster.StateStopped, nodeStates(st, "sind", "dev")["worker-3"])
	assert.Equal(t, cluster.StateStopped, nodeStates(st, "sind", "default")["controller"], "no cluster word: the default cluster")
	assert.Equal(t, cluster.StateMixed, st.Snapshot().ClustersOf("sind")[0].State)

	s.startWait(t, st, "power", "on", "worker-3.dev")
	assert.Equal(t, cluster.StateRunning, nodeStates(st, "sind", "dev")["worker-3"])
	s.startWait(t, st, "power", "freeze", "worker-3.dev")
	assert.Equal(t, cluster.StatePaused, nodeStates(st, "sind", "dev")["worker-3"])
	s.startWait(t, st, "power", "unfreeze", "worker-3.dev")
	assert.Equal(t, cluster.StateRunning, nodeStates(st, "sind", "dev")["worker-3"])
	s.startWait(t, st, "power", "reboot", "worker-3.dev")
	s.startWait(t, st, "power", "cycle", "worker-3.dev")
	s.startWait(t, st, "power", "cut", "worker-3.dev")
	assert.Equal(t, cluster.StateStopped, nodeStates(st, "sind", "dev")["worker-3"])
}

func TestSim_NodeErrors(t *testing.T) {
	s, st := newSim(t)
	task := s.startWait(t, st, "power", "on", "worker-[")
	assert.Equal(t, store.TaskFailed, task.Status, "a node set that does not parse")
	assert.Contains(t, task.Err, "worker-[")
	task = s.startWait(t, st, "power", "on", "worker-9.dev")
	assert.Equal(t, store.TaskFailed, task.Status)
	assert.Equal(t, "no node worker-9 in cluster dev", task.Err)
}

func TestSim_Attach(t *testing.T) {
	s, st := newSim(t)
	s.startWait(t, st, "connect", "container", "minio")
	m, _ := st.Snapshot().MeshOf("sind")
	assert.Equal(t, []string{"kind-control-plane", "minio"}, m.External)
	s.startWait(t, st, "disconnect", "container", "kind-control-plane")
	m, _ = st.Snapshot().MeshOf("sind")
	assert.Equal(t, []string{"minio"}, m.External)
	task := s.startWait(t, st, "connect", "container") // no name
	assert.Equal(t, store.TaskOK, task.Status)
}

func TestSim_UnknownCommands(t *testing.T) {
	s, st := newSim(t)
	task := s.startWait(t, st, "get", "cluster")
	assert.Equal(t, store.TaskOK, task.Status)
	r, _ := st.Snapshot().RunOfTrace(task.Trace)
	assert.Equal(t, "get cluster", r.Title)
	assert.Equal(t, []string{"cluster"}, stepsOf(r))

	task = s.startWait(t, st, "doctor")
	r, _ = st.Snapshot().RunOfTrace(task.Trace)
	assert.Equal(t, "doctor", r.Title)
	assert.Equal(t, []string{"run"}, stepsOf(r))
}

func TestSim_Cancel(t *testing.T) {
	w, st := seededStore(t)
	s := NewSim(st, w, liveClock{speed: 20})
	s.seq = 100
	require.NoError(t, s.SaveFile("/c/n.yaml", []byte(nightly)))
	id, err := s.Start(run.Spec{Argv: []string{"sind", "create", "cluster", "--config", "/c/n.yaml", "--data", "volume"}, Display: "sind create cluster"})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		task, _ := st.Snapshot().Task(id)
		r, ok := st.Snapshot().RunOfTrace(task.Trace)
		return ok && len(r.Root.Children) >= 3 // in the nodes step
	}, 10*time.Second, 5*time.Millisecond)

	require.NoError(t, s.Cancel(id))
	task, _ := st.Snapshot().Task(id)
	assert.True(t, task.Canceling || task.Done())
	task = waitTask(t, st, id)
	assert.Equal(t, store.TaskCanceled, task.Status)
	assert.Equal(t, run.ExitInterrupted, task.Exit)
	r, _ := st.Snapshot().RunOfTrace(task.Trace)
	assert.Equal(t, store.StatusCanceled, r.Status)
	assert.Equal(t, "rollback", r.Root.Children[len(r.Root.Children)-1].Name)
	_, ok := st.Snapshot().Cluster("ci", "nightly")
	assert.False(t, ok, "the rollback removed what was made")

	err = s.Cancel(id)
	assert.ErrorIs(t, err, run.ErrNotRunning)
	assert.ErrorIs(t, s.Cancel("nobody"), run.ErrNotRunning)
}

func TestSim_Lock(t *testing.T) {
	s, st := newSim(t)
	s.busy["sind"] = "sind create cluster other"
	id, err := s.Start(run.Spec{Argv: []string{"sind", "power", "on", "worker-1.legacy"}, Display: "sind power on worker-1.legacy"})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		task, _ := st.Snapshot().Task(id)
		r, ok := st.Snapshot().RunOfTrace(task.Trace)
		return ok && len(r.Root.Children) == 1 && r.Root.Children[0].Name == "realm lock"
	}, 10*time.Second, 5*time.Millisecond)
	wait := func() store.Span {
		task, _ := st.Snapshot().Task(id)
		r, _ := st.Snapshot().RunOfTrace(task.Trace)
		return r.Root.Children[0]
	}
	assert.Equal(t, "sind create cluster other", wait().Message, "it says whose lock it waits for")

	s.mu.Lock()
	delete(s.busy, "sind")
	s.mu.Unlock()
	task := waitTask(t, st, id)
	assert.Equal(t, store.TaskOK, task.Status)
	r, _ := st.Snapshot().RunOfTrace(task.Trace)
	assert.Equal(t, "realm lock", r.Root.Children[0].Name)
	assert.Equal(t, store.StatusOK, r.Root.Children[0].Status)
	assert.Equal(t, cluster.StateRunning, nodeStates(st, "sind", "legacy")["worker-1"])
}

func TestSim_LockHeldWhileRunning(t *testing.T) {
	w, st := seededStore(t)
	s := NewSim(st, w, liveClock{speed: 20})
	s.seq = 100
	id, err := s.Start(run.Spec{Argv: []string{"sind", "power", "reboot", "worker-0.dev"}, Display: "sind power reboot worker-0.dev"})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return st.Snapshot().Locks["sind"] != nil }, 10*time.Second, time.Millisecond)
	assert.Equal(t, "sind power reboot worker-0.dev", st.Snapshot().Locks["sind"].Command)
	waitTask(t, st, id)
	require.Eventually(t, func() bool { return st.Snapshot().Locks["sind"] == nil }, 10*time.Second, time.Millisecond)
}

func TestSim_LockWaitCanceled(t *testing.T) {
	s, st := newSim(t)
	s.clk = liveClock{speed: 1} // the wait lasts
	s.busy["sind"] = "other"
	id, err := s.Start(run.Spec{Argv: []string{"sind", "power", "on", "worker-1.legacy"}})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return s.Running() == 1 }, 10*time.Second, time.Millisecond)
	require.NoError(t, s.Cancel(id))
	task := waitTask(t, st, id)
	assert.Equal(t, store.TaskCanceled, task.Status)
	assert.Equal(t, cluster.StateStopped, nodeStates(st, "sind", "legacy")["worker-1"], "it never ran")
}

func TestSim_Shutdown(t *testing.T) {
	w, st := seededStore(t)
	s := NewSim(st, w, liveClock{speed: 1})
	id, err := s.Start(run.Spec{Argv: []string{"sind", "power", "reboot", "worker-0.dev"}})
	require.NoError(t, err)
	s.Shutdown(10 * time.Second)
	assert.Equal(t, 0, s.Running())
	task, _ := st.Snapshot().Task(id)
	assert.True(t, task.Done())

	// Patience runs out for a command that does not end.
	s.mu.Lock()
	s.wg.Add(1)
	s.mu.Unlock()
	start := time.Now()
	s.Shutdown(10 * time.Millisecond)
	assert.Less(t, time.Since(start), 5*time.Second)
	s.wg.Done()
}

func TestPositional(t *testing.T) {
	assert.Equal(t, []string{"dev", "x"}, positional([]string{"--config", "/c", "dev", "--pull", "--count", "2", "x", "-v"}))
	assert.Equal(t, "2", flagValue([]string{"--count", "2"}, "--count"))
	assert.Empty(t, flagValue([]string{"--count"}, "--count"), "a flag without its value")
	assert.Empty(t, flagValue(nil, "--count"))
	assert.Equal(t, "default", clusterWord(nil))
	assert.Equal(t, "dev", clusterWord([]string{"dev", "x"}))
}

func TestReady(t *testing.T) {
	assert.Equal(t, 14*time.Second, ready("controller", 0))
	assert.Equal(t, 19*time.Second, ready("worker", 0))
	assert.Equal(t, 19*time.Second, ready("worker", 6), "the pattern repeats")
	assert.Equal(t, 16*time.Second, ready("db", 1))
}
