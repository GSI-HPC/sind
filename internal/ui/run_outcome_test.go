// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"testing"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// outcomeStore returns a store with the clusters of the page tests: dev
// probed, with 2 of 4 workers taking jobs, and hpc probed as all good when
// good is set, or not probed at all.
func outcomeStore(good bool) *store.Store {
	st := store.New()
	pgFillStore(st)
	pgFillStatus(st)
	if good {
		svc := func(names ...string) cluster.ServiceHealth {
			h := cluster.ServiceHealth{}
			for _, n := range names {
				h[pgProbe(n)] = true
			}
			return h
		}
		node := func(name string, role config.Role, h cluster.ServiceHealth) *cluster.NodeStatus {
			return &cluster.NodeStatus{Name: name + ".hpc", Role: role, Managed: true, Health: &cluster.NodeHealth{State: cluster.StateRunning, Services: h}}
		}
		st.SetStatus("sind", "hpc", &cluster.Status{Name: "hpc", State: cluster.StateRunning, Nodes: []*cluster.NodeStatus{
			node("controller", config.RoleController, svc("munge", "slurmctld")),
			node("worker-0", config.RoleWorker, svc("munge", "slurmd")),
			node("worker-1", config.RoleWorker, svc("munge", "slurmd")),
			node("worker-2", config.RoleWorker, svc("munge", "slurmd")),
		}, Mounts: []cluster.MountPoint{{Path: "/data", Source: "/scratch/hpc", Type: config.StorageHostPath, OK: true, Data: true}}})
	}
	return st
}

// createdRun is the run of the recording of a create cluster that ended ok,
// of the cluster.
func createdRun(t *testing.T, name string) store.Run {
	t.Helper()
	run := runFixture(t, "create-ok.jsonl", -1)[0]
	run.Root.Node = name
	run.Title = "create cluster " + name
	run.Command = run.Title
	return run
}

func TestRunOutcomeOfACreate(t *testing.T) {
	snap := outcomeStore(true).Snapshot()
	o := runOutcomeOf(runCtx{Run: createdRun(t, "hpc"), Found: true, Snap: snap, Now: runAt(60)})
	require.NotNil(t, o)
	assert.Equal(t, "ok", o.Class)
	assert.Equal(t, "✓", o.Mark)
	assert.Equal(t, "hpc is ready for jobs", o.Title)
	assert.Equal(t, "created in 53s · 4 nodes", o.Meta)
	assert.Equal(t, "sind", o.Realm)
	assert.Equal(t, []pgAction{
		{Label: "All connect commands", URL: "/clusters/sind/hpc?tab=connect"},
		pgDialogAction("Add workers…", "", "create-worker", "realm", "sind", "cluster", "hpc"),
	}, o.Next)
	assert.Equal(t, "The cluster Summary shows the same checks.", o.NextNote)
	require.Len(t, o.Ready.Commands, 2)
	assert.Equal(t, pgCmd{Label: "1 · Open a shell on the controller", Text: "sind enter hpc"}, o.Ready.Commands[0])
	assert.Equal(t, "2 · In that shell, submit a test job and show the queue", o.Ready.Commands[1].Label)

	// Some workers take no jobs: ready, with how many do.
	o = runOutcomeOf(runCtx{Run: createdRun(t, "dev"), Found: true, Snap: snap, Now: runAt(60)})
	assert.Equal(t, "ok", o.Class)
	assert.Equal(t, "dev is ready for jobs, with 2 of 4 workers", o.Title)

	// Not probed yet: created, no more.
	o = runOutcomeOf(runCtx{Run: createdRun(t, "hpc"), Found: true, Snap: outcomeStore(false).Snapshot(), Now: runAt(60)})
	assert.Equal(t, "hpc was created", o.Title)
	assert.Equal(t, "ok", o.Class)
}

func TestRunOutcomeOfACreateThatCannotRunJobs(t *testing.T) {
	st := outcomeStore(false)
	sn := st.Snapshot()
	s := *sn.Status("sind", "dev")
	var nodes []*cluster.NodeStatus
	for _, n := range s.Nodes {
		if n != nil && n.Role == config.RoleController {
			c := *n
			c.Health = &cluster.NodeHealth{State: cluster.StateStopped}
			nodes = append(nodes, &c)
		}
	}
	s.Nodes = nodes
	st.SetStatus("sind", "dev", &s)
	o := runOutcomeOf(runCtx{Run: createdRun(t, "dev"), Found: true, Snap: st.Snapshot(), Now: runAt(60)})
	require.NotNil(t, o)
	assert.Equal(t, "warn", o.Class)
	assert.Equal(t, "!", o.Mark)
	assert.Equal(t, "dev was created, but it cannot run jobs", o.Title)
}

func TestRunOutcomeNone(t *testing.T) {
	snap := outcomeStore(true).Snapshot()
	at := func(run store.Run) *runOutcome {
		return runOutcomeOf(runCtx{Run: run, Found: true, Snap: snap, Now: runAt(60)})
	}
	mid := runFixture(t, "create-ok.jsonl", 59)[0]
	mid.Root.Node = "hpc"
	assert.Nil(t, at(mid), "a run that goes on")
	other := createdRun(t, "hpc")
	other.Root.Name = "power reboot"
	assert.Nil(t, at(other), "another command")
	assert.Nil(t, at(createdRun(t, "gone")), "a cluster that is gone")
	assert.Nil(t, at(createdRun(t, "")), "no cluster")
}

func TestRunOutcomeOfATaskWhoseClusterIsGone(t *testing.T) {
	snap := outcomeStore(true).Snapshot()
	run := createdRun(t, "gone")
	task := store.Task{ID: "t1", Trace: run.Trace, Realm: "sind", Cluster: "gone"}
	snap.Tasks = []store.Task{task}
	assert.Nil(t, runOutcomeOf(runCtx{Run: run, Found: true, Task: &task, Snap: snap, Now: runAt(60)}))
}

func TestRunPageOfACreateThatEndedOK(t *testing.T) {
	ts := runTestServer(t, runAt(60))
	pgFillStore(ts.st)
	pgFillStatus(ts.st)
	run := createdRun(t, "dev")
	run.ID = "r-dev"
	ts.st.SetRuns([]store.Run{run})
	body := pgBody(t, ts, "/runs/r-dev", 200)
	for _, want := range []string{
		`<section class="panel rout ok" aria-labelledby="rout-t">`,
		`<h2 id="rout-t">dev is ready for jobs, with 2 of 4 workers</h2>`,
		`created in 53s · 9 nodes · realm <span class="chip">sind</span>`,
		`1 · Open a shell on the submitter`,
		`<span class="rout-nl">Next</span>`,
		`href="/clusters/sind/dev?tab=connect"`,
		`The cluster Summary shows the same checks.`,
		// The command folds under the outcome, and Back leads on.
		`<details class="runfold"><summary><span class="runfold-l">Command</span><code>sind create cluster dev</code></summary>`,
	} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, body, `<div class="runcmd">`)
	h := runHeadOf(runCtx{Run: run, Found: true, Snap: ts.st.Snapshot(), Now: runAt(60)})
	assert.True(t, h.Fold)
	require.NotNil(t, h.Back)
	assert.True(t, h.Back.Primary)
}
