// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/slurm"
	"github.com/GSI-HPC/sind/pkg/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPgNodeNeed(t *testing.T) {
	sn := store.Snapshot{Clusters: map[string][]cluster.Summary{"r": {{Name: "c", State: cluster.StateMixed}}}}
	n := pgNode{Realm: "r", Cluster: "c", Short: "w", Arg: "w.c", Role: config.RoleWorker, State: cluster.StateUnknown}
	need := pgNodeNeed(sn, n, []pgNode{n})
	assert.Equal(t, "warn", need.Sev)
	assert.Equal(t, "Node", need.Label)
	assert.Equal(t, "w.c is in a state that sind does not know", need.Subject+" "+need.Title)
	assert.Contains(t, need.Detail, "a state that sind does not know. The cluster c is mixed.")
	assert.Equal(t, "Takes no jobs. No worker of c takes jobs.", need.Effect)
	assert.Len(t, need.Actions, 1)
	assert.Nil(t, need.Fix, "a node in an unknown state cannot be started or paused")
	assert.Equal(t, "need-node-r-w.c", need.ID())
	// A cluster that the snapshot does not hold.
	n.Cluster = "gone"
	assert.Contains(t, pgNodeNeed(sn, n, nil).Detail, "The cluster gone is unknown.")
	// A node that runs with two services down.
	n.State = cluster.StateRunning
	n.Services = []pgSvc{{"munge", false}, {"sshd", true}, {"slurmd", false}}
	need = pgNodeNeed(sn, n, nil)
	assert.Equal(t, pgNeed{Sev: "bad", Kind: pgNeedService, Title: "has munge, slurmd down", Detail: "The container runs, but munge and slurmd are not active."},
		pgNeed{Sev: need.Sev, Kind: need.Kind, Title: need.Title, Detail: need.Detail})
	assert.Equal(t, "Logs of slurmd", need.Actions[0].Label, "the journal of its Slurm service")
	assert.Equal(t, "Restart munge…", need.Fix.Label, "slurmd needs munge, so munge restarts first")
	assert.Equal(t, "Restart munge on w.c…", need.Fix.Name)
	assert.Contains(t, need.Fix.URL, "/dialogs/restart-service?")
	n.Services = []pgSvc{{"munge", false}, {"slurmd", true}}
	assert.Equal(t, "Logs of munge", pgNodeNeed(sn, n, nil).Actions[0].Label)
	// A node that runs and that Slurm sends no jobs.
	n.Services = []pgSvc{{"slurmd", true}}
	n.Slurm = &slurm.NodeInfo{Name: "w", State: "drained", Reason: "disk check"}
	need = pgNodeNeed(sn, n, nil)
	assert.Equal(t, pgNeed{Sev: "warn", Kind: pgNeedSlurm, Label: "Slurm", Title: "is drained in Slurm", Detail: "Slurm sends it no new jobs. Reason: disk check."},
		pgNeed{Sev: need.Sev, Kind: need.Kind, Label: need.Label, Title: need.Title, Detail: need.Detail})
	assert.Equal(t, "Resume…", need.Fix.Label)
	assert.Equal(t, "Resume w.c…", need.Fix.Name)
	assert.Equal(t, "Logs of slurmd", need.Actions[0].Label)
	n.Slurm.Reason = ""
	assert.Equal(t, "Slurm sends it no new jobs.", pgNodeNeed(sn, n, nil).Detail)
	// Paused and stopped.
	n.State, n.Slurm = cluster.StatePaused, nil
	assert.Equal(t, "Unfreeze…", pgNodeNeed(sn, n, nil).Fix.Label)
	n.State = cluster.StateStopped
	assert.Equal(t, "Start…", pgNodeNeed(sn, n, nil).Fix.Label)
}

func TestPgNodeEffect(t *testing.T) {
	worker := func(short string) pgNode {
		return pgNode{Cluster: "dev", Short: short, Role: config.RoleWorker, State: cluster.StateRunning, Known: true, Managed: true, Services: []pgSvc{{"slurmd", true}}}
	}
	w0, w1, w2 := worker("worker-0"), worker("worker-1"), worker("worker-2")
	w0.Services = []pgSvc{{"slurmd", false}}
	unmanaged := worker("other")
	unmanaged.Managed = false
	elsewhere := worker("worker-9")
	elsewhere.Cluster = "hpc"
	nodes := []pgNode{w0, w1, w2, unmanaged, elsewhere}
	assert.Equal(t, "Takes no jobs. dev runs jobs on 2 of 3 workers.", pgNodeEffect(w0, nodes))
	w1.Slurm = &slurm.NodeInfo{State: "draining"}
	assert.Equal(t, "Takes no new jobs, and ends the ones it runs. dev runs jobs on 2 of 3 workers.", pgNodeEffect(w1, nodes),
		"what Slurm says of the worker itself")

	ctl := pgNode{Cluster: "dev", Short: "controller", Role: config.RoleController, State: cluster.StateStopped}
	assert.Equal(t, "No new job starts on dev.", pgNodeEffect(ctl, []pgNode{ctl}))
	backup := pgNode{Cluster: "dev", Short: "controller-backup", Role: config.RoleController, State: cluster.StateRunning}
	assert.Equal(t, "The other controller schedules the jobs.", pgNodeEffect(ctl, []pgNode{ctl, backup, w1}))
	for role, want := range map[config.Role]string{
		config.RoleDB: "Jobs run, but sacct does not record them.", config.RoleAPI: "slurmrestd does not answer.",
		config.RoleSubmitter: "You cannot submit jobs from it.",
	} {
		assert.Equal(t, want, pgNodeEffect(pgNode{Role: role}, nil), role)
	}
}

func TestPgFailures(t *testing.T) {
	end := func(d time.Duration) *time.Time { e := pgNow.Add(-d); return &e }
	failed := store.Run{ID: "f", Title: "create cluster x", End: end(time.Hour), Status: store.StatusFailed}
	sn := store.Snapshot{Runs: []store.Run{failed}}
	assert.Len(t, pgFailures(sn, pgNow), 1)
	// A newer run of the same command that ended ok: the row is fixed for
	// fixedFor, then gone.
	ok := store.Run{ID: "ok", Title: "create cluster x", End: end(10 * time.Second), Status: store.StatusOK}
	sn.Runs = []store.Run{ok, failed}
	f := pgFailures(sn, pgNow)
	require.Len(t, f, 1)
	require.NotNil(t, f[0].Fixed)
	assert.Equal(t, "ok", f[0].Fixed.ID)
	need := pgRunNeed(sn, f[0], runEntryOf(sn, failed), pgNow)
	assert.Equal(t, pgNeedFixed, need.Kind)
	assert.Equal(t, "ok", need.Sev)
	assert.Empty(t, need.Dismiss)
	assert.Equal(t, "A newer run of the same command ended ok just now.", need.Detail)
	assert.Equal(t, pgNow.Add(20*time.Second).UnixMilli(), need.HideAt)
	assert.Empty(t, pgFailures(sn, pgNow.Add(30*time.Second)))
	// The same command that ended ok in another realm fixes nothing.
	sn.Tasks = []store.Task{{ID: "t1", Trace: "t-ok", Realm: "sind"}, {ID: "t2", Trace: "t-f", Realm: "ci"}}
	ok.Trace, failed.Trace = "t-ok", "t-f"
	sn.Runs = []store.Run{ok, failed}
	f = pgFailures(sn, pgNow)
	require.Len(t, f, 1)
	assert.Nil(t, f[0].Fixed)
	assert.False(t, pgSameRealm([]string{"ci"}, []string{"sind"}))
	assert.True(t, pgSameRealm([]string{"ci", "sind"}, []string{"sind"}))
	assert.True(t, pgSameRealm(nil, []string{"sind"}))
	// A dismissed one.
	sn.Runs = []store.Run{failed}
	sn.Dismissed = map[string]bool{"f": true}
	assert.Empty(t, pgFailures(sn, pgNow))
}

func TestPgRunNeedTryAgain(t *testing.T) {
	end := pgNow.Add(-time.Minute)
	failed := store.Run{ID: "f", Trace: "tr", Title: "create worker dev", End: &end, Status: store.StatusFailed, Err: "node worker-4: timed out\nmore", Root: store.Span{Node: "dev"}}
	sn := store.Snapshot{Clusters: map[string][]cluster.Summary{"sind": {{Name: "dev"}}}, Runs: []store.Run{failed}}
	// A run from a terminal: the dialog of its command, with what the log
	// names.
	need := pgRunNeed(sn, pgFailure{Run: failed}, runEntryOf(sn, failed), pgNow)
	assert.Equal(t, "node worker-4: timed out", need.Detail)
	require.NotNil(t, need.Fix)
	assert.Equal(t, "Try again…", need.Fix.Label)
	assert.True(t, need.Fix.Dialog)
	assert.Equal(t, "Show what failed", need.Actions[0].Label)
	assert.Equal(t, "f", need.Dismiss)
	// A task of sind ui without its form: Show what failed is the fix.
	sn.Tasks = []store.Task{{ID: "t", Action: "create-worker", Trace: "tr", Status: store.TaskFailed}}
	need = pgRunNeed(sn, pgFailure{Run: failed}, runEntryOf(sn, failed), pgNow)
	assert.Equal(t, "Show what failed", need.Fix.Label)
	assert.Empty(t, need.Actions)
	// With its form, the dialog of the task.
	sn.Tasks[0].Form = map[string][]string{"realm": {"sind"}, "cluster": {"dev"}}
	need = pgRunNeed(sn, pgFailure{Run: failed}, runEntryOf(sn, failed), pgNow)
	assert.Equal(t, "/dialogs/create-worker?task=t", need.Fix.URL)
}

func TestPgNeedsOfTheOverview(t *testing.T) {
	end := func(d time.Duration) *time.Time { e := pgNow.Add(-d); return &e }
	nodes := pgNodes("sind", "dev", map[config.Role]int{config.RoleController: 1, config.RoleWorker: 2}, map[string]cluster.State{"worker-1": cluster.StateStopped})
	ci := pgNodes("ci", "pr", map[config.Role]int{config.RoleController: 1, config.RoleWorker: 1}, nil)
	sn := store.Snapshot{
		Docker:   store.DockerState{Checked: true},
		Clusters: map[string][]cluster.Summary{"sind": {{Name: "dev", State: cluster.StateMixed}}, "ci": {{Name: "pr", State: cluster.StateRunning}}},
		Nodes:    map[string][]cluster.NodeSummary{"sind": nodes, "ci": ci},
		Locks: map[string]*state.LockHolder{
			"sind": {Command: "sind create worker dev", Host: "zeta", PID: 7, Since: pgNow.Add(-time.Hour)},
			"ci":   {Command: "sind create cluster pr", Host: "zeta", PID: 8, Since: pgNow.Add(-time.Minute)},
		},
		Tasks: []store.Task{
			// sind ui started worker-0 of dev a moment ago, which needed a
			// look then: it is fixed; worker-1 still needs one, and the
			// newer of two tasks that fixed worker-0 names its row.
			{ID: "o", Action: "power", Display: "sind power reboot worker-0.dev", Realm: "sind", Cluster: "dev", Status: store.TaskOK, End: end(9 * time.Second),
				Fixes: []string{"worker-0"}},
			{ID: "p", Action: "power", Display: "sind power on worker-0.dev", Realm: "sind", Cluster: "dev", Status: store.TaskOK, End: end(5 * time.Second),
				Fixes: []string{"worker-0", "worker-1", "gone"}},
			// One that fixed nothing, one that failed, one too long ago.
			{ID: "y", Action: "power", Realm: "sind", Cluster: "dev", Status: store.TaskOK, End: end(time.Second)},
			{ID: "f", Action: "power", Realm: "sind", Cluster: "dev", Status: store.TaskFailed, End: end(time.Second), Fixes: []string{"worker-0"}},
			{ID: "l", Action: "power", Realm: "sind", Cluster: "dev", Status: store.TaskOK, End: end(time.Minute), Fixes: []string{"worker-0"}},
			{ID: "c", Action: "create-cluster", Realm: "ci", Cluster: "pr", Trace: "tr-pr", Status: store.TaskRunning},
		},
		Runs: []store.Run{
			{ID: "pr", Trace: "tr-pr", Title: "create cluster pr", Start: pgNow.Add(-time.Minute)},
			{ID: "old", Title: "create cluster gone", End: end(time.Hour), Status: store.StatusFailed, Err: "no"},
			{ID: "dv", Title: "create worker dev", End: end(2 * time.Hour), Status: store.StatusFailed, Root: store.Span{Node: "dev"}},
		},
	}
	d := &pgOverview{}
	pgNeedsOf(d, sn, pgNow)
	var rows []string
	for _, n := range d.Needs {
		rows = append(rows, n.Kind+": "+n.Subject+" "+n.Title+" ["+n.Meta+"]")
	}
	assert.Equal(t, []string{
		"stopped: worker-1.dev is stopped [sind]",
		"run: create cluster gone failed 1 h ago []",
		"run: create worker dev failed 2 h ago [sind]",
		"lock: realm sind stays locked, but no command runs [sind]",
		"fixed: worker-0.dev is fixed [sind]",
	}, rows, "the lock of ci is the running create's; a stopped node is not fixed")
	assert.Equal(t, "sind power on worker-0.dev ended ok just now.", d.Needs[4].Detail)
	assert.Equal(t, "4 open · 1 fixed", d.NeedsText())
	assert.True(t, d.Open())
	require.Len(t, d.Running, 1)
	assert.Equal(t, "holds the lock of realm ci", d.Running[0].Lock)
	assert.Equal(t, "/activity?status=running", d.RunningLink())

	// Narrowed to ci: the run there, and no row.
	d = &pgOverview{Realm: "ci"}
	pgNeedsOf(d, sn, pgNow)
	assert.Empty(t, d.Needs)
	assert.False(t, d.Open())
	assert.Equal(t, "0 open", d.NeedsText())
	require.Len(t, d.Running, 1)
	assert.Equal(t, "/activity?realm=ci&status=running", d.RunningLink())
	d = &pgOverview{Realm: "sind"}
	pgNeedsOf(d, sn, pgNow)
	assert.Empty(t, d.Running)
	assert.Empty(t, d.Needs[0].Meta, "one realm names none")
	assert.Equal(t, "run/dv", d.Needs[1].Key)
	assert.Empty(t, d.Needs[1].Meta)

	// A run that waits for the lock says so, and does not hold it: the
	// lock of ci is stale, and the row names the run that waits for it.
	sn.Runs[0].Root.Children = []store.Span{{Kind: "wait", Name: runLockWait, State: "running", Started: pgNow}}
	d = &pgOverview{Realm: "ci"}
	pgNeedsOf(d, sn, pgNow)
	require.Len(t, d.Running, 1)
	assert.Equal(t, "waits for the lock of realm ci", d.Running[0].Lock)
	require.Len(t, d.Needs, 1)
	assert.Equal(t, "lock/ci", d.Needs[0].Key)
	assert.Equal(t, "create cluster pr waits for it.", d.Needs[0].Effect)
	sn.Runs = append(sn.Runs, store.Run{ID: "pw", Title: "create worker pr", Start: pgNow, Root: store.Span{Node: "pr", Children: sn.Runs[0].Root.Children}})
	d = &pgOverview{Realm: "ci"}
	pgNeedsOf(d, sn, pgNow)
	assert.Equal(t, "create cluster pr and create worker pr wait for it.", d.Needs[0].Effect)
	// A run of no known realm that does not wait may hold every lock, and
	// shows only for all realms.
	sn.Runs = append(sn.Runs, store.Run{ID: "t", Title: "doctor", Start: pgNow})
	d = &pgOverview{}
	pgNeedsOf(d, sn, pgNow)
	require.Len(t, d.Running, 3)
	assert.Empty(t, d.Running[0].Lock, "the newest first")
	assert.NotContains(t, d.Summary(), "stays locked")
	for _, n := range d.Needs {
		assert.NotEqual(t, pgNeedLock, n.Kind)
	}
}

func TestPgFixes(t *testing.T) {
	nodes := pgNodes("sind", "dev", map[config.Role]int{config.RoleController: 1, config.RoleWorker: 3}, map[string]cluster.State{"worker-1": cluster.StateStopped, "worker-2": cluster.StateStopped})
	sn := store.Snapshot{Clusters: map[string][]cluster.Summary{"sind": {{Name: "dev", State: cluster.StateMixed}}}, Nodes: map[string][]cluster.NodeSummary{"sind": nodes}}
	// The dialog sends a value of nodes for each node: those that need a
	// look are the ones it may fix, not a healthy one.
	task := store.Task{Action: "power", Realm: "sind", Cluster: "dev", Form: map[string][]string{"op": {"reboot"}, "nodes": {"worker-0", "worker-1", "worker-2,gone"}}}
	assert.Equal(t, []string{"worker-1", "worker-2"}, pgFixes(sn, task, pgNow))
	task.Form["op"] = []string{"stop"}
	assert.Empty(t, pgFixes(sn, task, pgNow), "shutting a node down fixes nothing")
	delete(task.Form, "op")
	assert.Empty(t, pgFixes(sn, task, pgNow), "a form without op fixes nothing")
	task.Action, task.Form["op"] = "create-worker", []string{"reboot"}
	assert.Empty(t, pgFixes(sn, task, pgNow))
}

func TestPgDismissRun(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	body := pgBody(t, ts, "/", 200)
	require.Contains(t, body, "create cluster hpc2</b> failed")
	assert.Equal(t, http.StatusNoContent, ts.post("/needs/runs/r4/dismiss", "").Code)
	body = pgBody(t, ts, "/", 200)
	assert.NotContains(t, body, "create cluster hpc2</b> failed")
	assert.Contains(t, body, `<span class="dot bad"></span><span class="pg-c">create cluster hpc2</span>`, "Recent activity keeps it")
	assert.Equal(t, http.StatusBadRequest, ts.post("/needs/runs/"+strings.Repeat("x", maxParamLen+1)+"/dismiss", "").Code)
}

func TestPgNodeNeedWithFaults(t *testing.T) {
	sn := store.Snapshot{Clusters: map[string][]cluster.Summary{"r": {{Name: "c", State: cluster.StateMixed}}}}
	at := time.Date(2026, 10, 21, 9, 2, 0, 0, time.Local)
	n := pgNode{Realm: "r", Cluster: "c", Short: "w", Arg: "w.c", Role: config.RoleWorker, State: cluster.StateRunning, Services: []pgSvc{{"slurmd", false}}}
	n.Fault = &store.Fault{Units: []store.Unit{{Name: "slurmd", Status: 1, Since: at}}}
	assert.Equal(t, "The container runs, but slurmd is not active. slurmd failed at 09:02 with exit status 1.", pgNodeNeed(sn, n, nil).Detail)
	n.Fault.Units[0].Status = 0
	assert.Equal(t, "The container runs, but slurmd is not active. slurmd failed at 09:02.", pgNodeNeed(sn, n, nil).Detail)
	// How its container stopped, and when, if Docker said.
	n.State = cluster.StateStopped
	n.Fault = &store.Fault{Exit: &store.Exit{Code: 137, OOMKilled: true, At: at}}
	assert.Equal(t, "The container stopped at 09:02 with exit code 137: the kernel killed it, as it ran out of memory. The cluster c is mixed.", pgNodeNeed(sn, n, nil).Detail)
	n.Fault.Exit.At = time.Time{}
	assert.Equal(t, "The container stopped with exit code 137: the kernel killed it, as it ran out of memory. The cluster c is mixed.", pgNodeNeed(sn, n, nil).Detail)
}
