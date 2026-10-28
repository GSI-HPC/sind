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
	assert.Equal(t, dockTaskCard(task, &run, runAt(30)).Title, h.Title)
}

func TestRunSummary(t *testing.T) {
	run := runFixture(t, "create-ok.jsonl", 59)[0]
	s := runSummaryOf(runCtxOf(run, runAt(25)))
	assert.Equal(t, runGanttOf(runCtxOf(run, runAt(25))).JSON(), s.Data)
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
	assert.Equal(t, "~ create cluster · sind ui", done.DocTitle)
	assert.Equal(t, "create cluster: stopped.", done.Say)

	bare := runSummaryOf(runCtxOf(runOf(0, ""), runAt(5)))
	assert.Zero(t, runSummaryOf(runCtxOf(store.Run{ID: "x"}, runAt(5))).SaySince, "a run without a start")
	assert.Equal(t, "▸ starting · sind ui", bare.DocTitle)
}

func TestRunFailedPanel(t *testing.T) {
	assert.Nil(t, runFailedOf(runCtxOf(runFixture(t, "create-ok.jsonl", -1)[0], runAt(60))))
	assert.Nil(t, runFailedOf(runCtxOf(runOf(0, ""), runAt(60))), "a run that goes on")

	p := runFailedOf(runCtxOf(runFixture(t, "create-failed.jsonl", -1)[0], runAt(400)))
	require.NotNil(t, p)
	assert.Equal(t, "bad", p.Class)
	assert.Equal(t, []runFailGroup{{Nodes: "worker-[1,2]", Class: "timeout", Err: "node {}: timed out; last probe error: probe munge: munge not ready"}}, p.Groups)
	assert.Empty(t, p.CmdErr)
	assert.Empty(t, p.Notes, "the line beside the outcome says that the rollback went well")

	// One node: its name comes back into the text.
	one := runOf(9, "failed", runSp("step", "nodes", runEndedAt(0, 9, "failed"), runKids(
		runSp("target", "worker-1", runEndedAt(0, 9, "failed"), runWith(func(s *store.Span) { s.Node, s.Err = "worker-1", "node worker-1: refused" })),
	)))
	p = runFailedOf(runCtxOf(one, runAt(10)))
	assert.Equal(t, "node worker-1: refused", p.Groups[0].Err)
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
	assert.Equal(t, "bad", p.Class)

	// A canceled run that had a failure says it was stopped.
	stopped := runOf(9, "canceled", runSp("step", "nodes", runEndedAt(0, 9, "canceled"), runKids(
		runSp("target", "w-1", runEndedAt(0, 1, "failed"), runWith(func(s *store.Span) { s.Err = "w-1: bad" })),
	)))
	p = runFailedOf(runCtxOf(stopped, runAt(10)))
	assert.Equal(t, "warn", p.Class)
}

func TestRunStoppedPanel(t *testing.T) {
	p := runFailedOf(runCtxOf(runFixture(t, "create-canceled.jsonl", -1)[0], runAt(400)))
	require.NotNil(t, p)
	assert.Equal(t, "warn", p.Class)
	assert.Equal(t, []string{"The run was interrupted. 2 targets stopped while they ran, and 1 target never started."}, p.Notes)

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
	assert.Equal(t, &pgCmd{Label: "What the event log names", Text: "sind create cluster bench"}, h.Command)
	assert.Equal(t, "Its flags and config are not in the log.", h.CmdNote)
	assert.Equal(t, "# The event log keeps no flags of this command; add them, such as --config FILE.\nsind create cluster bench", h.CopyNote)
	assert.Empty(t, h.Realm)
	assert.Equal(t, &runLink{Label: "Try again with a 10m wait…", Href: "/dialogs/create-cluster?name=bench&wait=10m", Primary: true}, h.Edit)
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
	power := run
	power.Title, power.Command, power.Root.Name = "power reboot worker-[0-1].bench", "power reboot bench", "power reboot"
	h = runHeadOf(runCtx{Run: power, Snap: snap})
	assert.Equal(t, "sind power reboot 'worker-[0-1].bench'", h.Command.Text, "the nodes, as the title names them")
	assert.Equal(t, "# The event log keeps no flags of this command; add the ones it had.\nsind power reboot 'worker-[0-1].bench'", h.CopyNote)
	power.Command = ""
	assert.Equal(t, []string{"power", "reboot", "worker-[0-1].bench"}, runLogWords(power), "no command: the title")
	workers := runOf(9, "ok", runSp("step", "nodes", runKids(
		runSp("target", "worker-2", runWith(func(s *store.Span) { s.Node, s.Role = "worker-2", "worker" })),
		runSp("target", "worker-3", runWith(func(s *store.Span) { s.Node, s.Role = "worker-3", "worker" })))))
	workers.Title, workers.Command, workers.Root.Name = "create worker worker-[2-3].bench", "create worker bench", "create worker"
	assert.Equal(t, []string{"create", "worker", "bench", "--count", "2"}, runLogWords(workers))
	// A run of another sind ui, whose task this one does not keep.
	workers.FromUI = true
	assert.Equal(t, "sind ui no longer keeps its form, and the log has no flags or config.", runHeadOf(runCtx{Run: workers, Snap: snap}).CmdNote)

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
