// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ctxOf returns the context of the fragments for a run at now.
func runCtxOf(run store.Run, now time.Time) runCtx {
	return runCtx{Run: run, Found: true, Now: now}
}

func TestRunFoldNodes(t *testing.T) {
	for name, tc := range map[string]struct {
		names []string
		want  string
	}{
		"one":              {[]string{"worker-2"}, "worker-2"},
		"two apart":        {[]string{"worker-5", "worker-2"}, "worker-[2,5]"},
		"two in a row":     {[]string{"worker-2", "worker-3"}, "worker-[2,3]"},
		"a range":          {[]string{"worker-0", "worker-1", "worker-2", "worker-3"}, "worker-[0-3]"},
		"range and single": {[]string{"w1", "w2", "w3", "w7"}, "w[1-3,7]"},
		"the same twice":   {[]string{"w2", "w2"}, "w2"},
		"no number":        {[]string{"controller", "controller"}, "controller"},
		"leading zero":     {[]string{"w01", "w02"}, "w0[1,2]"},
		"zero":             {[]string{"w0", "w1"}, "w[0,1]"},
		"mixed groups":     {[]string{"worker-1", "controller", "db-1", "worker-3"}, "worker-[1,3], controller, db-1"},
		"none":             {nil, ""},
	} {
		assert.Equal(t, tc.want, runFoldNodes(tc.names), name)
	}
}

func TestRunHead(t *testing.T) {
	snap := store.Snapshot{Clusters: nil}
	run := runOf(0, "")
	run.Program, run.Version = "sind", "v0.11.0"
	run.Trace = "4bf92f3577b34da6a3ce929d0e0e4736"

	// From a terminal: no task to stop, and the page says so.
	h := runHeadOf(runCtx{Run: run, Found: true, Snap: snap, Now: runAt(30)})
	assert.True(t, h.Terminal)
	assert.True(t, h.Running)
	assert.Equal(t, "from a terminal", h.Origin)
	assert.Contains(t, h.NoStop, "Ctrl-C")
	other := run
	other.Parent = "00f067aa0ba902b7"
	assert.Contains(t, runHeadOf(runCtx{Run: other, Found: true, Snap: snap, Now: runAt(30)}).NoStop, "Another program started this run")
	other.FromUI = true
	h2 := runHeadOf(runCtx{Run: other, Found: true, Snap: snap, Now: runAt(30)})
	assert.Equal(t, "from sind ui", h2.Origin)
	assert.Contains(t, h2.NoStop, "Another sind ui started this run")
	assert.Equal(t, "sind v0.11.0", h.Program)
	assert.Equal(t, "4bf92f35", h.Trace)
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", h.TraceTip)
	assert.Empty(t, h.TaskID)
	assert.Equal(t, "running", h.Pill)

	// From sind ui: the task is what Stop posts to; the cluster links when it exists.
	task := store.Task{ID: "t1", Trace: run.Trace, Realm: "sind", Cluster: "dev"}
	snap.Tasks = []store.Task{task}
	h = runHeadOf(newRunCtx((&View{Snap: snap, Now: runAt(30), Params: Params{}}).runWithRun(run)))
	assert.False(t, h.Terminal)
	assert.Equal(t, "from sind ui", h.Origin)
	assert.Empty(t, h.NoStop)
	assert.Equal(t, "t1", h.TaskID)
	assert.Equal(t, &runLink{Label: "Open Clusters", Href: "/clusters"}, h.Back, "no such cluster yet")

	rc := newRunCtx((&View{Snap: snap, Now: runAt(30), Params: Params{}}).runWithRun(run))
	require.NotNil(t, rc.Task)
	rc.Snap.Clusters = map[string][]cluster.Summary{"sind": {{Name: "other"}, {Name: "dev"}}}
	h = runHeadOf(rc)
	assert.Equal(t, &runLink{Label: "Open dev", Href: "/clusters/sind/dev"}, h.Back)

	// Stopping, and over: no task to stop any more; the exit code shows.
	rc.Task.Canceling = true
	assert.True(t, runHeadOf(rc).Canceling)
	over := runOf(5, "canceled")
	over.Root.Exit = new(int)
	*over.Root.Exit = 130
	rc = runCtx{Run: over, Found: true, Task: &task, Snap: rc.Snap, Now: runAt(30)}
	h = runHeadOf(rc)
	assert.Empty(t, h.TaskID)
	assert.False(t, h.Running)
	assert.Equal(t, "exit 130", h.Exit)
	assert.Equal(t, "canceled", h.Pill)
	assert.Equal(t, "1", runHeadOf(runCtx{Run: store.Run{Trace: "1"}}).Trace)
	assert.Empty(t, runHeadOf(runCtx{Run: store.Run{}}).Program)
}

func TestRunPageNamesTheRunAsTheDock(t *testing.T) {
	// Two runs of the same command differ by their flags, on the run page
	// as on their cards.
	run := runOf(0, "")
	run.Title = "create worker"
	task := store.Task{ID: "t1", Trace: run.Trace, Realm: "sind", Cluster: "dev", Argv: []string{"sind", "create", "worker", "dev", "--count", "3", "--realm", "sind"}}
	v := (&View{Snap: store.Snapshot{Tasks: []store.Task{task}}, Now: runAt(30), Params: Params{}}).runWithRun(run)
	rc := newRunCtx(v)
	assert.Equal(t, "create worker dev --count 3", rc.Run.Title)
	h := runHeadOf(rc)
	assert.Equal(t, "create worker dev --count 3", h.Title)
	require.NotNil(t, h.Stop)
	assert.Equal(t, "create worker dev --count 3", h.Stop.Title)
	assert.Equal(t, "create worker dev --count 3: started.", runSummaryOf(rc).Say)
	require.NoError(t, runPageLoad(v))
	assert.Equal(t, "create worker dev --count 3", v.Title)
	assert.Equal(t, dockTaskCard(task, run, true, runAt(30)).Title, h.Title)
}

func TestRunSummary(t *testing.T) {
	run := runFixture(t, "create-ok.jsonl", 59)[0]
	s := runSummaryOf(runCtxOf(run, runAt(25)))
	assert.Equal(t, "25s", s.Elapsed)
	assert.NotZero(t, s.Since)
	assert.Equal(t, "nodes 0/3 · controller ready munge", s.Line)
	assert.False(t, s.NoTargets)
	assert.Equal(t, []runCount{{"ok", "ok", 1}, {"running", "run", 3}, {"queued", "", 0}, {"failed", "bad", 0}, {"canceled", "warn", 0}}, s.Counts)
	assert.Equal(t, []string{"3 waits for a node to become ready. The slowest has used 5 % of its limit (5m00s)."}, s.Hints)
	assert.Equal(t, "▸ nodes 0/3 · sind ui", s.DocTitle)
	assert.Equal(t, "r:"+run.ID, s.SayKey)
	assert.Equal(t, runRunning, s.SayState)
	assert.Equal(t, "create cluster: started.", s.Say)
	assert.Equal(t, runAt(0).UnixMilli(), s.SaySince)

	// The run of a task: announced by the task, as the dock does.
	stopping := runCtxOf(run, runAt(25))
	stopping.Task = &store.Task{ID: "t1", Canceling: true}
	s = runSummaryOf(stopping)
	assert.Equal(t, "t:t1", s.SayKey)
	assert.Equal(t, "stopping", s.SayState)

	done := runSummaryOf(runCtxOf(runFixture(t, "create-canceled.jsonl", -1)[0], runAt(25)))
	assert.Empty(t, done.Hints)
	assert.Zero(t, done.Since)
	assert.Equal(t, "~ create cluster · sind ui", done.DocTitle)
	assert.Equal(t, "create cluster: stopped.", done.Say)
	assert.Len(t, done.Counts, 5, "no skipped count unless there are some")

	// Without targets there is nothing to count.
	bare := runSummaryOf(runCtxOf(runOf(0, ""), runAt(5)))
	assert.True(t, bare.NoTargets)
	assert.Zero(t, runSummaryOf(runCtxOf(store.Run{ID: "x"}, runAt(5))).SaySince, "a run without a start")
	assert.Equal(t, "▸ starting · sind ui", bare.DocTitle)
	skipped := runOf(0, "", runSp("step", "n", runStartedAt(0), runKids(runSp("target", "a", runEndedAt(0, 1, "skipped")))))
	assert.Contains(t, runSummaryOf(runCtxOf(skipped, runAt(5))).Counts, runCount{"skipped", "", 1})
}

func TestRunWaitHints(t *testing.T) {
	waits := func(n int, timeout time.Duration, start float64) []store.Span {
		var out []store.Span
		for range n {
			out = append(out, runSp("wait", "ready", runStartedAt(start), runWith(func(s *store.Span) { s.Timeout = timeout })))
		}
		return out
	}
	// One wait, at 60 % of its limit; waits without a limit, ended waits
	// and the waits of the command itself do not count as node waits.
	tgt := runSp("target", "w", runStartedAt(0), runKids(append(waits(1, 100*time.Second, 0),
		runSp("wait", "nolimit", runStartedAt(0)),
		runSp("wait", "old", runEndedAt(0, 5, "ok"), runWith(func(s *store.Span) { s.Timeout = time.Second })))...))
	run := runOf(0, "", runSp("wait", "realm lock", runStartedAt(0), runWith(func(s *store.Span) { s.Message = "x" })),
		runSp("wait", "idle", runStartedAt(0)),
		runSp("step", "nodes", runStartedAt(0), runKids(tgt)))
	assert.Equal(t, []string{
		"Waiting for realm lock: x.",
		"Waiting for idle.",
		"1 wait for a node to become ready. The slowest has used 60 % of its limit (1m40s).",
	}, runWaitHints(run, runAt(60)))
	assert.Empty(t, runWaitHints(runOf(0, ""), runAt(60)))

	// The slowest of several counts.
	two := runOf(0, "", runSp("step", "nodes", runStartedAt(0), runKids(
		runSp("target", "a", runStartedAt(0), runKids(waits(1, 100*time.Second, 50)...)),
		runSp("target", "b", runStartedAt(0), runKids(waits(1, 100*time.Second, 10)...)),
	)))
	assert.Equal(t, []string{"2 waits for a node to become ready. The slowest has used 50 % of its limit (1m40s)."}, runWaitHints(two, runAt(60)))
}

func TestRunFailedPanel(t *testing.T) {
	assert.Nil(t, runFailedOf(runCtxOf(runFixture(t, "create-ok.jsonl", -1)[0], runAt(60))))
	assert.Nil(t, runFailedOf(runCtxOf(runOf(0, ""), runAt(60))), "a run that goes on")

	p := runFailedOf(runCtxOf(runFixture(t, "create-failed.jsonl", -1)[0], runAt(400)))
	require.NotNil(t, p)
	assert.Equal(t, "What failed", p.Title)
	assert.Equal(t, "bad", p.Class)
	assert.Equal(t, []runFailGroup{{Nodes: "worker-[1,2]", Class: "timeout", Err: "node {}: timed out; last probe error: probe munge: munge not ready"}}, p.Groups)
	assert.Empty(t, p.CmdErr)
	assert.True(t, p.Doctor)
	assert.Equal(t, []string{"sind removed what the command had created (rollback). Nothing is left behind."}, p.Notes)

	// One node: its name comes back into the text.
	one := runOf(9, "failed", runSp("step", "nodes", runEndedAt(0, 9, "failed"), runKids(
		runSp("target", "worker-1", runEndedAt(0, 9, "failed"), runWith(func(s *store.Span) { s.Node, s.Err = "worker-1", "node worker-1: refused" })),
	)))
	p = runFailedOf(runCtxOf(one, runAt(10)))
	assert.Equal(t, "node worker-1: refused", p.Groups[0].Err)
	assert.False(t, p.Doctor)
	assert.Empty(t, p.Notes)

	// A failure with no failed target says what the command said.
	plain := runOf(9, "failed", runSp("step", "nodes", runEndedAt(0, 9, "failed")))
	plain.Err = "creating network: boom"
	p = runFailedOf(runCtxOf(plain, runAt(10)))
	assert.Empty(t, p.Groups)
	assert.Equal(t, "creating network: boom", p.CmdErr)

	// A target failed while the run goes on.
	live := runOf(0, "", runSp("step", "nodes", runStartedAt(0), runKids(
		runSp("target", "w-1", runEndedAt(0, 1, "failed"), runWith(func(s *store.Span) { s.Err = "w-1: bad" })),
	)))
	p = runFailedOf(runCtxOf(live, runAt(10)))
	require.NotNil(t, p)
	assert.Equal(t, "What failed", p.Title)

	// A canceled run that had a failure says it was stopped.
	stopped := runOf(9, "canceled", runSp("step", "nodes", runEndedAt(0, 9, "canceled"), runKids(
		runSp("target", "w-1", runEndedAt(0, 1, "failed"), runWith(func(s *store.Span) { s.Err = "w-1: bad" })),
	)))
	p = runFailedOf(runCtxOf(stopped, runAt(10)))
	assert.Equal(t, "Stopped", p.Title)
	assert.Equal(t, "warn", p.Class)
}

func TestRunStoppedPanel(t *testing.T) {
	p := runFailedOf(runCtxOf(runFixture(t, "create-canceled.jsonl", -1)[0], runAt(400)))
	require.NotNil(t, p)
	assert.Equal(t, "Stopped", p.Title)
	assert.Equal(t, "warn", p.Class)
	require.Len(t, p.Notes, 2)
	assert.Equal(t, "The run was interrupted. 2 targets stopped while they ran, and 1 target never started.", p.Notes[0])
	assert.Contains(t, p.Notes[1], "Nothing is left behind")

	// Singular, no rollback, and what finished is not stopped.
	one := runOf(9, "canceled", runSp("step", "nodes", runEndedAt(0, 9, "canceled"), runKids(
		runSp("call", "setup", runStartedAt(0)),
		runSp("target", "a", runEndedAt(0, 1, "ok")),
		runSp("target", "b", runStartedAt(0)),
		runSp("target", "c"),
	)))
	p = runFailedOf(runCtxOf(one, runAt(10)))
	assert.Equal(t, []string{"The run was interrupted. 1 target stopped while they ran, and 1 target never started."}, p.Notes)
}

func TestRunRollbackNote(t *testing.T) {
	note := func(rb store.Span) string { return runRollbackNote(runOf(9, "failed", rb)) }
	assert.Contains(t, note(runSp("step", "rollback", runEndedAt(1, 2, "ok"))), "Nothing is left behind")
	assert.Equal(t, "The rollback failed, so some of what the command created may be left behind. docker rm: busy",
		note(runSp("step", "rollback", runEndedAt(1, 2, "failed"), runWith(func(s *store.Span) { s.Err = "docker rm: busy" }))))
	assert.Equal(t, "The rollback failed, so some of what the command created may be left behind.",
		note(runSp("step", "rollback", runEndedAt(1, 2, "failed"))))
	assert.Contains(t, note(runSp("step", "rollback", runStartedAt(1))), "did not finish", "a rollback that the run left behind")
	assert.Contains(t, runRollbackNote(runOf(0, "", runSp("step", "rollback", runStartedAt(1)))), "is removing")
	assert.Contains(t, note(runSp("step", "rollback", runEndedAt(1, 2, "canceled"))), "did not finish")
	assert.Empty(t, note(runSp("step", "other", runEndedAt(1, 2, "ok"))))
	assert.Empty(t, runRollbackNote(runOf(9, "failed", runSp("call", "rollback"))))
	assert.Empty(t, runRollbackNote(runOf(9, "failed")))
}

func TestRunPlural(t *testing.T) {
	assert.Equal(t, "1 target", runPlural(1, "target"))
	assert.Equal(t, "0 targets", runPlural(0, "target"))
	assert.Equal(t, "2 targets", runPlural(2, "target"))
}

func TestRunBack(t *testing.T) {
	snap := store.Snapshot{Clusters: map[string][]cluster.Summary{"sind": {{Name: "dev"}}}}
	assert.Equal(t, &runLink{Label: "Open dev", Href: "/clusters/sind/dev"}, runBack(snap, store.Task{Realm: "sind", Cluster: "dev"}))
	assert.Equal(t, &runLink{Label: "Open Clusters", Href: "/clusters"}, runBack(snap, store.Task{Realm: "lab", Cluster: "dev"}))
	for _, a := range []string{"connect", "disconnect", "delete-realm"} {
		assert.Equal(t, &runLink{Label: "Open Mesh and DNS", Href: "/mesh"}, runBack(snap, store.Task{Realm: "sind", Action: a}), a)
	}
	assert.Nil(t, runBack(snap, store.Task{Action: "create-cluster"}))
}

func TestRunRetry(t *testing.T) {
	form := map[string][]string{"realm": {"sind"}, "cluster": {"dev"}, "count": {"2"}}
	failed := runFailed
	task := store.Task{ID: "t1", Action: "create-worker", Form: form}

	id, edit := runRetry(task, failed)
	assert.Equal(t, "t1", id)
	assert.Equal(t, &runLink{Label: "Edit and try again…", Href: "/dialogs/create-worker?task=t1"}, edit)

	id, edit = runRetry(task, runCanceled)
	assert.Equal(t, "t1", id, "a stopped run")
	assert.NotNil(t, edit)

	typed := store.Task{ID: "t2", Action: "delete-cluster", Form: form}
	id, edit = runRetry(typed, failed)
	assert.Empty(t, id, "the dialog asks for the name")
	assert.Equal(t, &runLink{Label: "Try again…", Href: "/dialogs/delete-cluster?task=t2"}, edit)

	for name, tc := range map[string]struct {
		task  store.Task
		class string
	}{
		"ok":        {task, runOK},
		"running":   {task, runRunning},
		"no form":   {store.Task{ID: "t3", Action: "create-worker"}, failed},
		"no action": {store.Task{ID: "t4", Action: "nope", Form: form}, failed},
	} {
		id, edit := runRetry(tc.task, tc.class)
		assert.Empty(t, id, name)
		assert.Nil(t, edit, name)
	}
}

func TestRunHeadWithoutTask(t *testing.T) {
	// A failed run from a terminal: the command of its title, a dialog
	// filled in from it, with a longer wait after a timeout, and the list
	// of clusters, as no realm has the cluster.
	run := runOf(9, "failed")
	run.Title, run.Root.Node, run.Class = "create cluster bench", "bench", "timeout"
	h := runHeadOf(runCtx{Run: run, Found: true, Snap: store.Snapshot{}, Now: runAt(30)})
	assert.Equal(t, &pgCmd{Label: "Command", Text: "sind create cluster bench"}, h.Command)
	assert.Contains(t, h.CmdNote, "not its flags")
	assert.Empty(t, h.Realm)
	assert.Equal(t, &runLink{Label: "Edit and try again…", Href: "/dialogs/create-cluster?name=bench&wait=10m"}, h.Edit)
	assert.Equal(t, &runLink{Label: "Open Clusters", Href: "/clusters"}, h.Back)
	assert.Empty(t, h.RetryID, "only a task runs again at once")

	// One realm has the cluster: the command names the realm, and the page
	// links to the cluster; without a timeout the wait is the dialog's.
	run.Class = ""
	snap := store.Snapshot{Clusters: map[string][]cluster.Summary{"ci": {{Name: "bench"}}}}
	h = runHeadOf(runCtx{Run: run, Found: true, Snap: snap, Now: runAt(30)})
	assert.Equal(t, "sind --realm ci create cluster bench", h.Command.Text)
	assert.Equal(t, "ci", h.Realm)
	assert.Equal(t, "/dialogs/create-cluster?name=bench&realm=ci", h.Edit.Href)
	assert.Equal(t, &runLink{Label: "Open bench", Href: "/clusters/ci/bench"}, h.Back)
	snap.Clusters = map[string][]cluster.Summary{"sind": {{Name: "bench"}}}
	assert.Equal(t, "sind create cluster bench", runHeadOf(runCtx{Run: run, Snap: snap}).Command.Text, "the default realm needs no flag")

	// A stopped create worker opens Add workers of its cluster, a timed
	// out node too; other commands get no dialog.
	run = runOf(9, "canceled")
	run.Title, run.Root.Node = "create worker bench", "bench"
	run.Root.Children = []store.Span{runSp("target", "worker-1", runEndedAt(0, 9, "failed"), runWith(func(s *store.Span) { s.Class = "timeout" }))}
	assert.True(t, runTimedOut(run))
	h = runHeadOf(runCtx{Run: run, Snap: snap})
	assert.Equal(t, "/dialogs/create-worker?cluster=bench&realm=sind", h.Edit.Href)
	run.Title = "delete cluster bench"
	assert.Nil(t, runHeadOf(runCtx{Run: run, Snap: snap}).Edit)
	run.Title = "create worker bench"
	assert.Nil(t, runHeadOf(runCtx{Run: run, Snap: store.Snapshot{}}).Edit, "Add workers needs the realm")

	// A run that went well, or goes on, offers no dialog; one without a
	// title no command.
	ok := runOf(9, "ok")
	ok.Root.Node = "dev"
	h = runHeadOf(runCtx{Run: ok, Snap: store.Snapshot{}})
	assert.NotNil(t, h.Command)
	assert.Nil(t, h.Edit)
	assert.Nil(t, runHeadOf(runCtx{Run: store.Run{}}).Command)
	assert.False(t, runTimedOut(ok))
}
