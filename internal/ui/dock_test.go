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
	f := tasksFragment()
	assert.True(t, f.Shell)
	assert.Equal(t, []string{"run/"}, f.Prefixes)
	assert.Equal(t, []store.Topic{store.TopicTasks}, f.Topics)
	assert.Equal(t, []string{"run", "task"}, f.Params)
}

// dockOf returns the dock for the snapshot and the params.
func runDockOf(snap store.Snapshot, now time.Time, kv ...string) dockData {
	v := &View{Snap: snap, Now: now, Params: Params{}}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Params.Set(kv[i], kv[i+1])
	}
	return dockLoad(v)
}

func TestRunDockTaskWithRun(t *testing.T) {
	run := runFixture(t, "create-ok.jsonl", 59)[0]
	task := store.Task{ID: "t1", Display: "sind create cluster dev", Trace: run.Trace, Start: runAt(0), Status: store.TaskRunning}
	d := runDockOf(store.Snapshot{Runs: []store.Run{run}, Tasks: []store.Task{task}}, runAt(25))
	require.Len(t, d.Cards, 1)
	c := d.Cards[0]
	assert.Equal(t, runRunning, c.Class)
	assert.Equal(t, "create cluster", c.Title, "the command as the log names it")
	assert.Equal(t, "/runs/"+run.ID, c.Href)
	assert.Equal(t, "nodes 0/3 · controller ready munge", c.Line)
	assert.Equal(t, "25s", c.Elapsed)
	assert.NotZero(t, c.Since)
	assert.NotNil(t, c.Stop)
	assert.False(t, c.Dismiss)
	assert.Empty(t, c.Chip)
	assert.Equal(t, "Show progress", c.Action)
	assert.Equal(t, "t:t1", c.SayKey)
	assert.Equal(t, runRunning, c.SayState)
	assert.Equal(t, "create cluster: started.", c.Say)
	assert.Len(t, c.Segs, 3, "pull images, preflight, which ran for more than a second, and nodes")

	// Stopping: no second Stop.
	task.Canceling = true
	c = runDockOf(store.Snapshot{Runs: []store.Run{run}, Tasks: []store.Task{task}}, runAt(25)).Cards[0]
	assert.Nil(t, c.Stop)
	assert.True(t, c.Canceling)

	// The page of the run hides the card, by run id or by task id.
	snap := store.Snapshot{Runs: []store.Run{run}, Tasks: []store.Task{task}}
	assert.Empty(t, runDockOf(snap, runAt(25), "run", run.ID).Cards)
	d = runDockOf(snap, runAt(25), "task", "t1")
	assert.Empty(t, d.Cards)
	assert.False(t, d.Bar, "nothing else to sum up")
	assert.Equal(t, []dockPart{{"1 running", true}}, d.Parts)
	assert.Len(t, runDockOf(snap, runAt(25), "run", "other", "task", "other").Cards, 1)
}

func TestRunDockTaskEnded(t *testing.T) {
	failed := runFixture(t, "create-failed.jsonl", -1)[0]
	task := store.Task{ID: "t", Display: "d", Trace: failed.Trace, Start: runAt(0), End: failed.End, Status: store.TaskFailed, Err: "node worker-1: timed out\nmore", Output: "Error: node worker-1: timed out\nmore"}
	c := runDockOf(store.Snapshot{Runs: []store.Run{failed}, Tasks: []store.Task{task}}, runAt(900)).Cards[0]
	assert.Equal(t, runFailed, c.Class)
	assert.Equal(t, "✗", c.Mark)
	assert.Equal(t, "Show what failed", c.Action)
	assert.Equal(t, "failed in 5m16s", c.Line)
	assert.Equal(t, "5m16s", c.Elapsed)
	assert.Zero(t, c.Since)
	assert.True(t, c.Dismiss)
	assert.Nil(t, c.Stop)
	assert.Equal(t, "node worker-1: timed out", c.Why, "the first line of the error")
	assert.True(t, c.Compact)

	// The command ended, and its task not yet: the run says how.
	task.Status, task.End = store.TaskRunning, nil
	c = runDockOf(store.Snapshot{Runs: []store.Run{failed}, Tasks: []store.Task{task}}, runAt(900)).Cards[0]
	assert.Equal(t, runFailed, c.Class)
	assert.False(t, c.Dismiss, "it can be stopped, not closed")
	assert.NotEmpty(t, c.Why, "the error of the run")

	ok := runFixture(t, "create-ok.jsonl", -1)[0]
	task = store.Task{ID: "t", Trace: ok.Trace, Status: store.TaskOK, Action: "create-cluster"}
	c = runDockOf(store.Snapshot{Runs: []store.Run{ok}, Tasks: []store.Task{task}}, runAt(900)).Cards[0]
	assert.Equal(t, runOK, c.Class)
	assert.Equal(t, "create cluster", c.Title)
	assert.Equal(t, "done in 53s", c.Line)
	assert.False(t, c.Compact)
	assert.Empty(t, c.Why)
}

func TestRunDockTaskWithoutRun(t *testing.T) {
	now := runAt(100)
	for name, tc := range map[string]struct {
		task    store.Task
		class   string
		line    string
		since   bool
		title   string
		cancel  bool
		stop    bool
		dismiss bool
		action  string
	}{
		"starting": {store.Task{ID: "a", Display: "sind create cluster", Start: runAt(90), Status: store.TaskRunning},
			runRunning, "starting", true, "create cluster", true, false, false, "Show progress"},
		"stopping": {store.Task{ID: "a", Action: "power", Start: runAt(90), Status: store.TaskRunning, Canceling: true},
			runRunning, "stopping", true, "power", false, true, false, "Show progress"},
		"no start": {store.Task{ID: "a", Status: store.TaskRunning},
			runRunning, "starting", false, "", true, false, false, "Show progress"},
		"ok": {store.Task{ID: "a", Display: "d", Start: runAt(90), End: runPtr(runAt(95)), Status: store.TaskOK},
			runOK, "done in 5.0s", false, "d", false, false, true, "Details"},
		"failed": {store.Task{ID: "a", Display: "d", Start: runAt(90), End: runPtr(runAt(95)), Status: store.TaskFailed},
			runFailed, "failed in 5.0s", false, "d", false, false, true, "Show what failed"},
		"canceled": {store.Task{ID: "a", Display: "d", Start: runAt(90), End: runPtr(runAt(95)), Status: store.TaskCanceled},
			runCanceled, "canceled in 5.0s", false, "d", false, false, true, "Details"},
		"odd": {store.Task{ID: "a", Display: "d", Start: runAt(90), End: runPtr(runAt(95)), Status: "weird"},
			runEnded, "ended in 5.0s", false, "d", false, false, true, "Details"},
		"odd without end": {store.Task{ID: "a", Display: "d", Start: runAt(90), Status: "weird"},
			runRunning, "starting", true, "d", true, false, true, "Show progress"},
	} {
		d := runDockOf(store.Snapshot{Tasks: []store.Task{tc.task}}, now)
		require.Len(t, d.Cards, 1, name)
		c := d.Cards[0]
		assert.Equal(t, tc.class, c.Class, name)
		assert.Equal(t, tc.line, c.Line, name)
		assert.Equal(t, tc.since, c.Since != 0, name)
		assert.Equal(t, tc.title, c.Title, name)
		assert.Equal(t, tc.cancel, c.Stop != nil, name)
		assert.Equal(t, tc.stop, c.Canceling, name)
		assert.Equal(t, tc.dismiss, c.Dismiss, name)
		assert.Equal(t, tc.action, c.Action, name)
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
		Tasks: []store.Task{{ID: "t", Trace: hasTask.Trace, Status: store.TaskOK, Start: runAt(0)}},
	}
	d := runDockOf(snap, runAt(25))
	require.Len(t, d.Cards, 2, "the task and the one run that goes on without one")
	assert.Equal(t, "t", d.Cards[1].TaskID, "the run that goes on comes first")
	c := d.Cards[0]
	assert.Equal(t, "terminal", c.Chip)
	assert.Equal(t, "r:"+run.ID, c.SayKey)
	assert.Empty(t, c.TaskID)
	assert.Nil(t, c.Stop)
	assert.False(t, c.Dismiss)
	assert.Equal(t, "/runs/"+run.ID, c.Href)
	assert.Equal(t, "nodes 0/3 · controller ready munge", c.Line)
	// The page of the run draws its card no more, and the bar still
	// counts it.
	d = runDockOf(snap, runAt(25), "run", run.ID)
	require.Len(t, d.Cards, 1)
	assert.Equal(t, "t", d.Cards[0].TaskID)
	assert.True(t, d.Bar)
	assert.Equal(t, []dockPart{{"1 running", true}, {"1 done", false}}, d.Parts)
}

func TestRunDockFit(t *testing.T) {
	now := runAt(100)
	card := func(class string, id int) dockCard {
		return dockCard{Class: class, Title: fmt.Sprint(id), start: runAt(float64(id)), end: runAt(float64(10 + id))}
	}
	titles := func(d dockData) []string {
		var out []string
		for _, c := range d.Cards {
			out = append(out, c.Title)
		}
		return out
	}
	// One card: no bar.
	d := dockFit([]dockCard{card(runRunning, 1)}, now)
	assert.Equal(t, []string{"1"}, titles(d))
	assert.False(t, d.Bar)
	assert.False(t, d.Toggle)

	// The commands that run first, oldest first; then those that ended,
	// newest first, the ones that ended ok last.
	d = dockFit([]dockCard{card(runOK, 1), card(runRunning, 3), card(runFailed, 4), card(runRunning, 2), card(runCanceled, 5), card(runOK, 6)}, now)
	assert.Equal(t, []string{"2", "3", "5", "4", "6", "1"}, titles(d))
	assert.True(t, d.Bar)
	assert.True(t, d.Toggle)
	assert.True(t, d.Running)
	assert.Equal(t, []dockPart{{"2 running", true}, {"1 failed", false}, {"1 stopped", false}, {"2 done", false}}, d.Parts)
	assert.Zero(t, d.More)

	// A command that waits for the lock of its realm comes after one that
	// works.
	waits := card(runRunning, 1)
	waits.waiting = true
	assert.Equal(t, []string{"2", "1"}, titles(dockFit([]dockCard{waits, card(runRunning, 2)}, now)))

	// A card of a command that ended ok a while ago is counted, not drawn.
	gone := card(runOK, 7)
	gone.HideAt, gone.Dismiss = now.UnixMilli(), true
	soon := card(runOK, 8)
	soon.HideAt = now.UnixMilli() + 1
	d = dockFit([]dockCard{card(runRunning, 1), gone, soon}, now)
	assert.Equal(t, []string{"1", "8"}, titles(d))
	assert.True(t, d.Bar)
	assert.True(t, d.Clear)
	assert.Equal(t, []dockPart{{"1 running", true}, {"2 done", false}}, d.Parts)
	d = dockFit([]dockCard{gone, gone}, now)
	assert.Empty(t, d.Cards)
	assert.False(t, d.Bar, "nothing to show")
	assert.Equal(t, []dockPart{{"2 done", false}}, d.Parts)
	assert.Equal(t, []dockPart{{"1 ended", false}}, dockFit([]dockCard{card(runEnded, 1)}, now).Parts)

	// The last cards that do not fit are counted.
	var all []dockCard
	for i := range 9 {
		all = append(all, card(runRunning, i))
	}
	d = dockFit(all, now)
	assert.Equal(t, []string{"0", "1", "2", "3", "4", "5"}, titles(d))
	assert.Equal(t, 3, d.More)
	assert.Empty(t, dockFit(nil, now).Cards)
}

func TestRunDockHidesDoneCards(t *testing.T) {
	task := store.Task{ID: "a", Display: "sind create worker dev", Start: runAt(0), End: runPtr(runAt(10)), Status: store.TaskOK}
	c := runDockOf(store.Snapshot{Tasks: []store.Task{task}}, runAt(12)).Cards[0]
	assert.Equal(t, runAt(10).Add(dockHideAfter).UnixMilli(), c.HideAt)
	assert.Empty(t, runDockOf(store.Snapshot{Tasks: []store.Task{task}}, runAt(18)).Cards)
	task.Status = store.TaskFailed
	c = runDockOf(store.Snapshot{Tasks: []store.Task{task}}, runAt(100)).Cards[0]
	assert.Zero(t, c.HideAt, "a failure stays")
}

func TestTaskTitle(t *testing.T) {
	task := store.Task{
		Action: "create-worker", Realm: "ci", Cluster: "dev", Display: "sind --realm ci create worker dev --count 3 --cpus 4",
		Argv: []string{"sind", "--realm", "ci", "create", "worker", "dev", "--count", "3", "--cpus", "4"},
	}
	assert.Equal(t, "create worker dev --count 3 --cpus 4", taskTitle(task, nil))
	assert.Equal(t, "create worker dev --count 3 --cpus 4", taskTitle(task, &store.Run{Title: "create worker"}), "the cluster of the task")

	create := store.Task{Action: "create-cluster", Cluster: "lab", Display: "sind create cluster --config /c/lab.yaml --pull --wait 10m"}
	assert.Equal(t, "create cluster lab --pull --wait 10m", taskTitle(create, nil), "the words of Display; the name, not the config")
	assert.Equal(t, "create cluster lab --pull --wait 10m", taskTitle(create, &store.Run{Title: "create cluster lab"}))
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
		require.Len(t, d.Cards, 2)
		for _, c := range d.Cards {
			assert.Equal(t, tc.line, c.Line, "%s at %v", c.Title, tc.now)
			require.Len(t, c.Segs, tc.segs)
		}
		assert.Equal(t, tc.line == "mesh", strings.HasPrefix(d.Cards[0].Segs[0].Title, "mesh:"), "%v", d.Cards[0].Segs)
	}
}
