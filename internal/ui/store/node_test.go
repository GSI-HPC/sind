// SPDX-License-Identifier: LGPL-3.0-or-later

package store

import (
	"testing"
	"time"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/GSI-HPC/sind/pkg/slurm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetFaults(t *testing.T) {
	s := New()
	sub := s.Notifier().Subscribe()
	defer sub.Close()
	f := Fault{Units: []Unit{{Name: "slurmd", State: "failed", Result: "exit-code", Status: 1}}}
	s.SetFaults("r", "a", map[string]Fault{"worker-4": f})
	assert.Equal(t, []Topic{StatusTopic("r", "a")}, sub.Take(), "the faults are part of the status")
	got, ok := s.Snapshot().Fault("r", "a", "worker-4")
	require.True(t, ok)
	u, ok := got.Unit("slurmd")
	require.True(t, ok)
	assert.Equal(t, 1, u.Status)
	_, ok = got.Unit("munge")
	assert.False(t, ok)
	_, ok = s.Snapshot().Fault("r", "a", "worker-0")
	assert.False(t, ok)

	s.SetFaults("r", "a", map[string]Fault{"worker-4": f})
	assert.Empty(t, sub.Take(), "the same faults are no change")
	s.SetFaults("r", "b", map[string]Fault{"db": {}})
	s.KeepStatuses(StatusKey{"r", "b"})
	_, ok = s.Snapshot().Fault("r", "a", "worker-4")
	assert.False(t, ok, "kept with the status")
	s.SetFaults("r", "b", nil)
	assert.Empty(t, s.Snapshot().Faults)
}

// clockAt returns a clock that tells the time *at.
func clockAt(at *time.Time) func() time.Time { return func() time.Time { return *at } }

func TestChangesOfTheContainers(t *testing.T) {
	s := New()
	at := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	s.SetClock(clockAt(&at))
	w0 := cluster.NodeSummary{Container: "r-dev-worker-0", Cluster: "dev", State: cluster.StateRunning}
	w1 := cluster.NodeSummary{Container: "r-dev-worker-1", Cluster: "dev", State: cluster.StateRunning}
	s.SetNodes("r", []cluster.NodeSummary{w0, w1})
	assert.Empty(t, s.Snapshot().NodeChanges, "what a first list holds is no change")

	at = at.Add(time.Minute)
	w0.State = cluster.StateStopped
	s.SetNodes("r", []cluster.NodeSummary{w0, w1})
	at = at.Add(time.Minute)
	w0.State = cluster.StateRunning
	s.SetNodes("r", []cluster.NodeSummary{w0, w1})
	held := s.Snapshot()
	assert.Equal(t, []Change{
		{At: at, Source: SourceDocker, Subject: "container", State: "running", OK: true},
		{At: at.Add(-time.Minute), Source: SourceDocker, Subject: "container", State: "stopped"},
	}, held.Changes("r", "dev", "worker-0"), "the newest first")
	assert.Empty(t, held.Changes("r", "dev", "worker-1"))

	s.SetNodes("r", []cluster.NodeSummary{w1})
	assert.Empty(t, s.Snapshot().Changes("r", "dev", "worker-0"), "a node that went takes its changes along")
	assert.Len(t, held.Changes("r", "dev", "worker-0"), 2, "a published snapshot never changes")
	s.SetNodes("r", nil)
	assert.Empty(t, s.Snapshot().NodeChanges)
}

func TestChangesKeepTheNewest(t *testing.T) {
	s := New()
	n := cluster.NodeSummary{Container: "r-dev-worker-0", Cluster: "dev", State: cluster.StateRunning}
	s.SetNodes("r", []cluster.NodeSummary{n})
	for i := range MaxChanges + 5 {
		n.State = []cluster.State{cluster.StatePaused, cluster.StateRunning}[i%2]
		s.SetNodes("r", []cluster.NodeSummary{n})
	}
	got := s.Snapshot().Changes("r", "dev", "worker-0")
	require.Len(t, got, MaxChanges)
	assert.Equal(t, "paused", got[0].State, "the newest, which time.Now timed")
	assert.False(t, got[0].At.IsZero())
}

func TestChangesOfTheServices(t *testing.T) {
	s := New()
	at := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	s.SetClock(clockAt(&at))
	status := func(slurmd bool, state cluster.State) *cluster.Status {
		return &cluster.Status{Name: "dev", Nodes: []*cluster.NodeStatus{
			nil,
			{Name: "db.dev"},
			{Name: "worker-4.dev", Health: &cluster.NodeHealth{State: state, Services: cluster.ServiceHealth{probe.ServiceMunge: true, probe.ServiceSlurmd: slurmd}}},
		}}
	}
	s.SetStatus("r", "dev", status(true, cluster.StateRunning))
	s.SetStatus("r", "dev", status(false, cluster.StateRunning))
	at = at.Add(time.Minute)
	s.SetStatus("r", "dev", status(true, cluster.StateRunning))
	// A node that stops has no services up, which is not a service that
	// went down.
	s.SetStatus("r", "dev", status(false, cluster.StateStopped))
	s.SetStatus("r", "dev", status(true, cluster.StateRunning))
	// A new service and a node new in the status are no change.
	s.SetStatus("r", "dev", &cluster.Status{Name: "dev", Nodes: []*cluster.NodeStatus{
		{Name: "worker-4.dev", Health: &cluster.NodeHealth{State: cluster.StateRunning, Services: cluster.ServiceHealth{probe.ServiceMunge: true, probe.ServiceSlurmd: true, probe.ServiceSSHD: false}}},
		{Name: "worker-5.dev", Health: &cluster.NodeHealth{State: cluster.StateRunning}},
	}})
	assert.Equal(t, []Change{
		{At: at, Source: SourceSystemd, Subject: "slurmd", State: "active", OK: true},
		{At: at.Add(-time.Minute), Source: SourceSystemd, Subject: "slurmd", State: "down"},
	}, s.Snapshot().Changes("r", "dev", "worker-4"))
	s.SetStatus("r", "dev", nil)
	assert.Len(t, s.Snapshot().Changes("r", "dev", "worker-4"), 2, "a status that went is no change")
}

func TestChangesInSlurm(t *testing.T) {
	s := New()
	at := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	s.SetClock(clockAt(&at))
	info := func(nodes ...slurm.NodeInfo) *Slurm { return &Slurm{Info: &cluster.SlurmInfo{Nodes: nodes}} }
	s.SetSlurm("r", "dev", info(slurm.NodeInfo{Name: "worker-5", State: "idle"}))
	s.SetSlurm("r", "dev", info(slurm.NodeInfo{Name: "worker-5", State: "idle"}))
	s.SetSlurm("r", "dev", &Slurm{Err: "sinfo: boom"})
	s.SetSlurm("r", "dev", info(slurm.NodeInfo{Name: "worker-5", State: "idle"}))
	assert.Empty(t, s.Snapshot().NodeChanges, "sinfo that did not answer is no change")

	s.SetSlurm("r", "dev", info(slurm.NodeInfo{Name: "worker-5", State: "drained", Reason: "memory errors"}, slurm.NodeInfo{Name: "worker-6", State: "idle"}))
	s.SetSlurm("r", "dev", info(slurm.NodeInfo{Name: "worker-5", State: "drained", Reason: "memory errors"}, slurm.NodeInfo{Name: "worker-6", State: "idle", CPUs: 2}))
	s.SetSlurm("r", "dev", info(slurm.NodeInfo{Name: "worker-5", State: "idle"}, slurm.NodeInfo{Name: "worker-6", State: "idle"}))
	assert.Equal(t, []Change{
		{At: at, Source: SourceSlurm, Subject: "Slurm", State: "idle", OK: true},
		{At: at, Source: SourceSlurm, Subject: "Slurm", State: "drained", Reason: "memory errors"},
	}, s.Snapshot().Changes("r", "dev", "worker-5"))
	assert.Empty(t, s.Snapshot().Changes("r", "dev", "worker-6"), "a node new in sinfo, and other columns, are no change")
}
