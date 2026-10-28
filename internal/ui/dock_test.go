// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunTasksFragmentDeclaration(t *testing.T) {
	r := NewRegistry()
	registerDock(r)
	for _, name := range []string{"tasks", "tasks-menu"} {
		f := r.fragments[name]
		assert.Equal(t, name, f.Name)
		assert.Equal(t, []string{"run/"}, f.Prefixes)
		assert.Equal(t, []store.Topic{store.TopicTasks}, f.Topics)
		assert.Equal(t, []string{"run", "task"}, f.Params)
	}
}

// runDockOf returns the dock for the snapshot and the params.
func runDockOf(snap store.Snapshot, now time.Time, kv ...string) dockData {
	v := &View{Snap: snap, Now: now, Params: Params{}, CSRF: "tok"}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Params.Set(kv[i], kv[i+1])
	}
	return dockLoad(v)
}

func TestRunDockTaskWithRun(t *testing.T) {
	run := runFixture(t, "create-ok.jsonl", 59)[0]
	task := store.Task{ID: "t1", Display: "sind create cluster dev", Trace: run.Trace, Start: runAt(0), Status: store.TaskRunning}
	d := runDockOf(store.Snapshot{Runs: []store.Run{run}, Tasks: []store.Task{task}}, runAt(25))
	require.Len(t, d.Running, 1)
	assert.Empty(t, d.Ended)
	c := d.Running[0]
	assert.Equal(t, runRunning, c.Class)
	assert.Equal(t, "create cluster", c.Title, "the command as the log names it")
	assert.Equal(t, "/runs/"+run.ID, c.Href)
	assert.Equal(t, "nodes 0/3 · controller waits for munge", c.Line)
	assert.Equal(t, "25s", c.Elapsed)
	assert.NotZero(t, c.Since)
	assert.Zero(t, c.HideAt)
	require.NotNil(t, c.Stop)
	assert.Equal(t, "tok", c.Stop.CSRF)
	assert.Equal(t, "tok", c.CSRF)
	assert.False(t, c.Dismiss)
	assert.Empty(t, c.Chip)
	assert.Empty(t, c.Result, "it has not ended")
	assert.Empty(t, c.When)
	assert.Equal(t, "Show progress", c.Action)
	assert.Equal(t, "t:t1", c.SayKey)
	assert.Equal(t, runRunning, c.SayState)
	assert.Equal(t, "create cluster: started.", c.Say)
	assert.Len(t, c.Segs, 3, "pull images, preflight, which ran for more than a second, and nodes")
	require.NotNil(t, d.Strip, "the strip shows the command that runs")
	assert.Equal(t, "t1", d.Strip.TaskID)
	assert.Empty(t, d.StripMore, "nothing else to list")
	assert.Equal(t, []dockPart{{N: 1, Word: "running", Class: "run"}}, d.Parts)
	assert.Equal(t, "Commands: 1 running", d.Label)

	// Stopping: no second Stop.
	task.Canceling = true
	c = runDockOf(store.Snapshot{Runs: []store.Run{run}, Tasks: []store.Task{task}}, runAt(25)).Running[0]
	assert.Nil(t, c.Stop)
	assert.True(t, c.Canceling)

	// The page of the run leaves it off the strip, by run id or by task
	// id; the list still has it.
	snap := store.Snapshot{Runs: []store.Run{run}, Tasks: []store.Task{task}}
	assert.Nil(t, runDockOf(snap, runAt(25), "run", run.ID).Strip)
	d = runDockOf(snap, runAt(25), "task", "t1")
	assert.Nil(t, d.Strip)
	assert.Len(t, d.Running, 1)
	assert.Equal(t, []dockPart{{N: 1, Word: "running", Class: "run"}}, d.Parts)
	assert.NotNil(t, runDockOf(snap, runAt(25), "run", "other", "task", "other").Strip)
}

func TestRunDockTaskEnded(t *testing.T) {
	failed := runFixture(t, "create-failed.jsonl", -1)[0]
	task := store.Task{ID: "t", Display: "d", Trace: failed.Trace, Start: runAt(0), End: failed.End, Status: store.TaskFailed, Err: "node worker-1: timed out\nmore", Output: "Error: node worker-1: timed out\nmore"}
	d := runDockOf(store.Snapshot{Runs: []store.Run{failed}, Tasks: []store.Task{task}}, runAt(900))
	require.Len(t, d.Ended, 1)
	c := d.Ended[0]
	assert.Equal(t, runFailed, c.Class)
	assert.Equal(t, "✗", c.Mark)
	assert.Equal(t, "Show what failed", c.Action)
	assert.Equal(t, "5m16s", c.Elapsed)
	assert.Zero(t, c.Since)
	assert.True(t, c.Dismiss)
	assert.Nil(t, c.Stop)
	result, class := runResult(failed, runAt(900))
	assert.Equal(t, result, c.Result, "what came of the run, as Activity says")
	assert.Equal(t, "bad", c.ResultClass)
	assert.Equal(t, class, c.ResultClass)
	assert.Equal(t, runAt(316).Local().Format("15:04"), c.When)
	assert.Equal(t, "create cluster: failed. node worker-1: timed out\nmore", c.Say)
	assert.Equal(t, 1, d.Failed)
	assert.Equal(t, []dockPart{{N: 1, Word: "failed", Class: "bad"}}, d.Parts)
	assert.Equal(t, "Commands: 1 failed", d.Label)
	assert.Nil(t, d.Strip, "it ended long ago")

	// The command ended, and its task not yet: the run says how.
	task.Status, task.End = store.TaskRunning, nil
	c = runDockOf(store.Snapshot{Runs: []store.Run{failed}, Tasks: []store.Task{task}}, runAt(900)).Ended[0]
	assert.Equal(t, runFailed, c.Class)
	assert.False(t, c.Dismiss, "it can be stopped, not closed")
	assert.Equal(t, result, c.Result)
	assert.NotEmpty(t, c.When, "when the run ended")

	ok := runFixture(t, "create-ok.jsonl", -1)[0]
	task = store.Task{ID: "t", Trace: ok.Trace, Status: store.TaskOK, Action: "create-cluster"}
	c = runDockOf(store.Snapshot{Runs: []store.Run{ok}, Tasks: []store.Task{task}}, runAt(900)).Ended[0]
	assert.Equal(t, runOK, c.Class)
	assert.Equal(t, "create cluster", c.Title)
	assert.Equal(t, "3 nodes ready for jobs", c.Result)
	assert.Empty(t, c.ResultClass)
	assert.Nil(t, c.Open, "no cluster to open")

	// A new cluster that is ready: the card opens it.
	task.Realm, task.Cluster = "sind", "dev"
	c = runDockOf(store.Snapshot{Runs: []store.Run{ok}, Tasks: []store.Task{task}}, runAt(900)).Ended[0]
	assert.Equal(t, &runLink{Label: "Open dev", Href: "/clusters/sind/dev", Primary: true}, c.Open)
}

func TestRunDockTaskWithoutRun(t *testing.T) {
	now := runAt(100)
	end := runPtr(runAt(95))
	for name, tc := range map[string]struct {
		task    store.Task
		class   string
		line    string
		result  string
		rclass  string
		since   bool
		title   string
		cancel  bool
		stop    bool
		dismiss bool
		action  string
	}{
		"starting": {store.Task{ID: "a", Display: "sind create cluster", Start: runAt(90), Status: store.TaskRunning},
			runRunning, "starting", "", "", true, "create cluster", true, false, false, "Show progress"},
		"stopping": {store.Task{ID: "a", Action: "power", Start: runAt(90), Status: store.TaskRunning, Canceling: true},
			runRunning, "stopping", "", "", true, "power", false, true, false, "Show progress"},
		"no start": {store.Task{ID: "a", Status: store.TaskRunning},
			runRunning, "starting", "", "", false, "", true, false, false, "Show progress"},
		"ok": {store.Task{ID: "a", Display: "d", Start: runAt(90), End: end, Status: store.TaskOK},
			runOK, "done in 5.0s", "done in 5.0s", "", false, "d", false, false, true, "Details"},
		"failed": {store.Task{ID: "a", Display: "d", Start: runAt(90), End: end, Status: store.TaskFailed},
			runFailed, "failed in 5.0s", "failed in 5.0s", "bad", false, "d", false, false, true, "Show what failed"},
		"failed with an error": {store.Task{ID: "a", Display: "d", Start: runAt(90), End: end, Status: store.TaskFailed, Err: " exit status 1\nmore"},
			runFailed, "failed in 5.0s", "exit status 1", "bad", false, "d", false, false, true, "Show what failed"},
		"canceled": {store.Task{ID: "a", Display: "d", Start: runAt(90), End: end, Status: store.TaskCanceled},
			runCanceled, "stopped in 5.0s", "stopped in 5.0s", "warn", false, "d", false, false, true, "Details"},
		"odd": {store.Task{ID: "a", Display: "d", Start: runAt(90), End: end, Status: "weird"},
			runEnded, "ended in 5.0s", "ended in 5.0s", "", false, "d", false, false, true, "Details"},
		"odd without end": {store.Task{ID: "a", Display: "d", Start: runAt(90), Status: "weird"},
			runRunning, "starting", "", "", true, "d", true, false, true, "Show progress"},
	} {
		c := dockTaskCard(tc.task, nil, now)
		assert.Equal(t, tc.class, c.Class, name)
		assert.Equal(t, tc.line, c.Line, name)
		assert.Equal(t, tc.result, c.Result, name)
		assert.Equal(t, tc.rclass, c.ResultClass, name)
		assert.Equal(t, tc.since, c.Since != 0, name)
		assert.Equal(t, tc.title, c.Title, name)
		assert.Equal(t, tc.cancel, c.Stop != nil, name)
		assert.Equal(t, tc.stop, c.Canceling, name)
		assert.Equal(t, tc.dismiss, c.Dismiss, name)
		assert.Equal(t, tc.action, c.Action, name)
		assert.Equal(t, tc.task.End != nil, c.When != "", name)
		assert.Equal(t, "/tasks/a", c.Href, name)
		assert.Len(t, c.Segs, 1, name)
	}
}

func runPtr[T any](v T) *T { return &v }

func TestRunDockTerminalRuns(t *testing.T) {
	run := runFixture(t, "create-ok.jsonl", 59)[0]
	run.Parent = ""
	over := runFixture(t, "create-failed.jsonl", -1)[0]
	over.ID = "over"
	hasTask := runFixture(t, "interleaved.jsonl", -1)[0]
	snap := store.Snapshot{
		Runs:  []store.Run{run, over, hasTask},
		Tasks: []store.Task{{ID: "t", Trace: hasTask.Trace, Status: store.TaskOK, Start: runAt(0), End: runPtr(runAt(5))}},
	}
	d := runDockOf(snap, runAt(25))
	require.Len(t, d.Running, 1, "the one run that goes on without a task")
	require.Len(t, d.Ended, 1, "the task; a run that ended without one is for Activity")
	assert.Equal(t, "t", d.Ended[0].TaskID)
	c := d.Running[0]
	assert.Equal(t, "terminal", c.Chip)
	assert.Equal(t, "r:"+run.ID, c.SayKey)
	assert.Empty(t, c.TaskID)
	assert.Nil(t, c.Stop)
	assert.False(t, c.Dismiss)
	assert.Equal(t, "/runs/"+run.ID, c.Href)
	assert.Equal(t, "nodes 0/3 · controller waits for munge", c.Line)
	assert.Equal(t, run.ID, strings.TrimPrefix(d.Strip.Href, "/runs/"))
	assert.Equal(t, "1 ended", d.StripMore)
	assert.Equal(t, "Commands: 1 running, 1 done", d.Label)
	// The page of the run leaves it off the strip, and the task ended
	// long ago.
	d = runDockOf(snap, runAt(25), "run", run.ID)
	assert.Nil(t, d.Strip)
	assert.Empty(t, d.StripMore)
	assert.Equal(t, []dockPart{{N: 1, Word: "running", Class: "run"}}, d.Parts)
}

func TestRunDockFit(t *testing.T) {
	now := runAt(100)
	card := func(class string, id int) dockCard {
		return dockCard{Class: class, Title: fmt.Sprint(id), start: runAt(float64(id)), end: runAt(float64(10 + id))}
	}
	titles := func(cards []dockCard) []string {
		var out []string
		for _, c := range cards {
			out = append(out, c.Title)
		}
		return out
	}
	// The commands that run, the one that started last first, a command
	// that waits for the lock of its realm too; then those that ended, the
	// one that ended last first.
	waits := card(runRunning, 7)
	waits.waiting = true
	d := dockFit([]dockCard{card(runOK, 1), card(runRunning, 3), card(runFailed, 4), waits, card(runCanceled, 5), card(runOK, 6)}, now)
	assert.Equal(t, []string{"7", "3"}, titles(d.Running))
	assert.Equal(t, []string{"6", "5", "4", "1"}, titles(d.Ended))
	assert.Zero(t, d.More)
	assert.Equal(t, "Ended today", d.EndedLabel)
	assert.Equal(t, 1, d.Failed)
	assert.Equal(t, []dockPart{{N: 2, Word: "running", Class: "run"}, {N: 1, Word: "failed", Class: "bad"}}, d.Parts)
	assert.Equal(t, "Commands: 2 running, 1 failed, 1 stopped, 2 done", d.Label)
	require.NotNil(t, d.Strip)
	assert.Equal(t, "3", d.Strip.Title, "the command that works, not the one that started last and waits")
	assert.Equal(t, "1 more running · 4 ended", d.StripMore)
	// With only commands that wait, the strip shows the one that started
	// last of them.
	waits2 := card(runRunning, 2)
	waits2.waiting = true
	assert.Equal(t, "7", dockFit([]dockCard{waits2, waits}, now).Strip.Title)

	// Without a command that runs, the strip shows the one that ended last
	// for a while, until run.js takes it off.
	d = dockFit([]dockCard{card(runOK, 85), card(runFailed, 80)}, now)
	require.NotNil(t, d.Strip)
	assert.Equal(t, "85", d.Strip.Title)
	assert.Equal(t, runAt(95).Add(dockStripFor).UnixMilli(), d.Strip.HideAt)
	assert.Zero(t, d.Ended[0].HideAt, "the list keeps it")
	assert.Equal(t, "1 more ended", d.StripMore)
	assert.Equal(t, []dockPart{{N: 1, Word: "failed", Class: "bad"}}, d.Parts)
	assert.Nil(t, dockFit([]dockCard{card(runOK, 85)}, runAt(95).Add(dockStripFor)).Strip, "after it, the strip goes")
	assert.Empty(t, dockFit([]dockCard{card(runOK, 85)}, now).StripMore)
	// A command of the page, or one without an end, gives way to the next.
	here := card(runOK, 88)
	here.here = true
	assert.Equal(t, "85", dockFit([]dockCard{here, card(runOK, 85)}, now).Strip.Title)
	noEnd := card(runEnded, 1)
	noEnd.end = time.Time{}
	assert.Nil(t, dockFit([]dockCard{noEnd}, now).Strip)
	runningHere := card(runRunning, 9)
	runningHere.here = true
	assert.Nil(t, dockFit([]dockCard{runningHere}, now).Strip)

	// With only commands that ended ok or were stopped, the button counts
	// them as ended.
	d = dockFit([]dockCard{card(runOK, 1), card(runCanceled, 2)}, now)
	assert.Equal(t, []dockPart{{N: 2, Word: "ended"}}, d.Parts)
	assert.Equal(t, "Commands: 2 ended: 1 stopped, 1 done", d.Label, "the name starts with what the button says")
	assert.Zero(t, d.Failed)

	// One that ended before today: the list says Ended.
	old := card(runOK, 1)
	old.end = now.AddDate(0, 0, -1)
	assert.Equal(t, "Ended", dockFit([]dockCard{old}, now).EndedLabel)

	// The list shows the last dockMax that ended, and counts the rest.
	var all []dockCard
	for i := range 9 {
		all = append(all, card(runOK, i))
	}
	d = dockFit(all, now)
	assert.Equal(t, []string{"8", "7", "6", "5", "4", "3"}, titles(d.Ended))
	assert.Equal(t, 3, d.More)
	assert.Equal(t, []dockPart{{N: 6, Word: "ended"}}, d.Parts, "it counts what the list shows")

	d = dockFit(nil, now)
	assert.Empty(t, d.Parts)
	assert.Empty(t, d.Label)
	assert.Nil(t, d.Strip)
}

func TestTaskTitle(t *testing.T) {
	task := store.Task{
		Action: "create-worker", Realm: "ci", Cluster: "dev", Display: "sind --realm ci create worker dev --count 3 --cpus 4",
		Argv: []string{"sind", "--realm", "ci", "create", "worker", "dev", "--count", "3", "--cpus", "4"},
	}
	assert.Equal(t, "create worker dev --count 3 --cpus 4", taskTitle(task, nil))
	restart := store.Task{Action: "restart-service", Cluster: "dev", Form: map[string][]string{"unit": {"slurmd"}},
		Argv: []string{"sind", "ssh", "worker-4.dev", "--", "systemctl", "restart", "slurmd"}}
	assert.Equal(t, "restart slurmd worker-4.dev", taskTitle(restart, nil), "a restart says what it restarts")
	restart.Form = nil
	assert.Equal(t, "ssh worker-4.dev -- systemctl restart slurmd", taskTitle(restart, nil))
	assert.Equal(t, "create worker dev --count 3 --cpus 4", taskTitle(task, &store.Run{Title: "create worker"}), "the cluster of the task")

	create := store.Task{Action: "create-cluster", Cluster: "lab", Display: "sind create cluster --config /c/lab.yaml --pull --wait 10m"}
	assert.Equal(t, "create cluster lab --pull --wait 10m", taskTitle(create, nil), "the words of Display; the name, not the config")
	assert.Equal(t, "create cluster lab --pull --wait 10m", taskTitle(create, &store.Run{Title: "create cluster lab"}))
	name, flags := taskTitleParts(create, nil)
	assert.Equal(t, []string{"create cluster lab", "--pull --wait 10m"}, []string{name, flags}, "the name and the flags apart")
	create.Display = "sind create cluster --config=/c/lab.yaml --realm=ci"
	assert.Equal(t, "create cluster lab", taskTitle(create, nil))

	assert.Equal(t, "power", taskTitle(store.Task{Action: "power"}, nil))
	assert.Equal(t, "d", taskTitle(store.Task{Display: "d"}, nil))
	assert.Empty(t, taskTitle(store.Task{}, nil))
}

func TestRunTaskHelpers(t *testing.T) {
	assert.False(t, taskEnded(store.Task{Status: store.TaskRunning}))
	assert.True(t, taskEnded(store.Task{Status: store.TaskRunning, End: runPtr(runAt(1))}))
	assert.True(t, taskEnded(store.Task{Status: store.TaskFailed}))
	assert.True(t, taskEnded(store.Task{}))
	assert.Zero(t, taskElapsed(store.Task{}, runAt(5)))
	assert.Equal(t, 5*time.Second, taskElapsed(store.Task{Start: runAt(0)}, runAt(5)))
	assert.Equal(t, 2*time.Second, taskElapsed(store.Task{Start: runAt(0), End: runPtr(runAt(2))}, runAt(5)))
	assert.Zero(t, taskElapsed(store.Task{Start: runAt(9)}, runAt(5)))
}

func TestRunDockShowsAHiddenStepThatTakesLong(t *testing.T) {
	// The step mesh is hidden; once it has run for a second, as docker
	// pulls the images of the mesh, the cards show it as the terminal does.
	mesh := runSp("step", "mesh", runStartedAt(1), runWith(func(s *store.Span) { s.Hidden = true }))
	run := runOf(0, "", mesh)
	task := store.Task{ID: "t1", Trace: run.Trace, Start: runAt(0), Status: store.TaskRunning}
	terminal := run
	terminal.ID, terminal.Trace = "r2", "tr2"
	for _, tc := range []struct {
		now  float64
		line string
		segs int
	}{{1.5, "working", 1}, {3, "mesh", 1}} {
		d := runDockOf(store.Snapshot{Runs: []store.Run{run, terminal}, Tasks: []store.Task{task}}, runAt(tc.now))
		require.Len(t, d.Running, 2)
		for _, c := range d.Running {
			assert.Equal(t, tc.line, c.Line, "%s at %v", c.Title, tc.now)
			require.Len(t, c.Segs, tc.segs)
		}
		assert.Equal(t, tc.line == "mesh", strings.HasPrefix(d.Running[0].Segs[0].Title, "mesh:"), "%v", d.Running[0].Segs)
	}
}

func TestRunDockTemplates(t *testing.T) {
	ts := runTestServer(t, runNow)
	runStore(t, ts)
	end := runNow.Add(-50 * time.Second)
	ts.st.AddTask(store.Task{ID: "t-f", Display: "sind power reboot worker-4.dev", Realm: "sind", Cluster: "dev", Trace: "tr-f",
		Start: runNow.Add(-time.Minute), End: &end, Status: store.TaskFailed, Err: "exit status 1\nmore"})

	// The strip: the command that runs, its steps, its line and clock,
	// what else the list holds, and its buttons.
	strip := runFragment(t, ts, runNow, "tasks")
	for _, want := range []string{
		`<div class="tstrip running" role="group" aria-label="The command that runs">`,
		`<span class="ts-t"><b><a href="/runs/r-mid" title="sind create cluster dev">create cluster dev</a></b></span>`,
		`<span class="ts-s"><span class="ts-l">nodes 0/3 · controller waits for munge</span><span class="num" data-since="`,
		`<div class="segbar" role="progressbar" aria-label="Progress of create cluster dev"`,
		`<button type="button" class="btn ghost sm ts-more" data-dock-toggle aria-controls="cmds-pop" aria-expanded="false">1 ended</button>`,
		`<button type="button" class="ts-f" data-dock-toggle aria-controls="cmds-pop" aria-expanded="false" aria-label="1 failed: show the commands"><i class="dot bad" aria-hidden="true"></i>1</button>`,
		`<summary class="btn sm" aria-label="Stop create cluster dev…">Stop…</summary>`,
		`<a class="btn sm primary" href="/runs/r-mid" aria-label="Show progress: create cluster dev">Show progress</a></span>`,
		`<button type="button" class="btn sm ts-show" data-dock-toggle aria-controls="cmds-pop" aria-expanded="false" aria-label="Show the commands">Show</button>`,
	} {
		assert.Contains(t, strip, want)
	}
	assert.NotContains(t, strip, "data-say-key", "the list marks the announcements")

	// The button and the list: what runs, then what ended, with how.
	menu := runFragment(t, ts, runNow, "tasks-menu")
	for _, want := range []string{
		`<button type="button" class="cmds-btn" data-dock-toggle aria-controls="cmds-pop" aria-expanded="false" aria-label="Commands: 1 running, 1 failed">` +
			`<span class="cmds-p run"><span class="spin" aria-hidden="true"></span>1<span class="u"> running</span></span><span class="sep" aria-hidden="true"></span>` +
			`<span class="cmds-p bad"><i class="dot bad" aria-hidden="true"></i>1<span class="u"> failed</span></span></button>`,
		`<section class="cmds-pop" id="cmds-pop" role="dialog" aria-labelledby="cmds-h" tabindex="-1">`,
		`<div class="cmds-hd"><h2 id="cmds-h">Commands</h2><a href="/activity">All in Activity</a></div>`,
		`<div class="cmds-g"><h3>Running</h3><div class="cmds-r running"><span hidden data-say-key="t:t-mid" data-say-state="running"`,
		`<span class="cmds-s">nodes 0/3 · controller waits for munge</span>`,
		`<div class="cmds-g"><h3>Ended today</h3><div class="cmds-r failed">`,
		`<span class="cmds-t"><a href="/tasks/t-f" title="sind power reboot worker-4.dev">power reboot worker-4.dev</a><span class="chip" title="realm">sind</span></span>`,
		`<span class="cmds-w num">` + end.Local().Format("15:04") + `</span><form class="inline" method="post" action="/tasks/t-f/dismiss"`,
		`aria-label="Clear power reboot worker-4.dev" title="Clear">×</button></form><span class="cmds-s bad">exit status 1</span>`,
		`<button type="submit" class="btn sm">Clear ended</button></form><span class="faint">A command from a terminal shows while it runs.</span>`,
	} {
		assert.Contains(t, menu, want)
	}

	// With nothing that runs, the strip shows the command that ended last
	// for a while, and a new cluster to open.
	ts = runTestServer(t, runNow)
	ok := runFixtureAs(t, "create-ok.jsonl", -1, "r-ok", "trace-ok")
	ts.st.SetRuns([]store.Run{ok})
	ts.st.AddTask(store.Task{ID: "t-ok", Action: "create-cluster", Realm: "sind", Cluster: "dev", Display: "sind create cluster dev --pull", Trace: "trace-ok",
		Start: runAt(0), End: runPtr(runNow.Add(-5 * time.Second)), Status: store.TaskOK})
	strip = runFragment(t, ts, runNow, "tasks")
	assert.Contains(t, strip, `<div class="tstrip ok" data-hide-at="`+fmt.Sprint(runNow.Add(10*time.Second).UnixMilli())+`" role="group" aria-label="The command that ended">`)
	assert.Contains(t, strip, `<span class="mark ok" aria-hidden="true">✓</span>`)
	assert.Contains(t, strip, `title="sind create cluster dev --pull">create cluster dev<span class="fl"> --pull</span></a></b><span class="chip" title="realm">sind</span>`, "the flags apart from the name")
	assert.Contains(t, strip, `<span class="ts-s"><span class="ts-r">sind ·</span><span class="ts-l" title="3 nodes ready for jobs">3 nodes ready for jobs</span></span>`)
	assert.Contains(t, strip, `<a class="btn sm" href="/runs/r-ok" aria-label="Details: create cluster dev --pull">Details</a><a class="btn sm primary" href="/clusters/sind/dev">Open dev</a>`)
	assert.NotContains(t, strip, "ts-more", "nothing else to list")
	menu = runFragment(t, ts, runNow, "tasks-menu")
	assert.Contains(t, menu, `aria-label="Commands: 1 ended: 1 done"><span class="cmds-p"><i class="dot" aria-hidden="true"></i>1<span class="u"> ended</span></span></button>`)
	assert.Contains(t, menu, `<span class="cmds-s">3 nodes ready for jobs · <a href="/clusters/sind/dev">Open dev</a></span>`)

	// Nothing to show: no strip, no button.
	ts = runTestServer(t, runNow)
	assert.Empty(t, strings.TrimSpace(runFragment(t, ts, runNow, "tasks")))
	assert.Empty(t, strings.TrimSpace(runFragment(t, ts, runNow, "tasks-menu")))
}
