// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
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
	assert.True(t, c.Cancel)
	assert.False(t, c.Dismiss)
	assert.Empty(t, c.Chip)
	assert.Equal(t, "Show progress", c.Action)
	assert.Equal(t, "t:t1", c.SayKey)
	assert.Equal(t, runRunning, c.SayState)
	assert.Equal(t, "create cluster: started.", c.Say)
	assert.Len(t, c.Segs, 2)

	// Stopping: no second Stop.
	task.Canceling = true
	c = runDockOf(store.Snapshot{Runs: []store.Run{run}, Tasks: []store.Task{task}}, runAt(25)).Cards[0]
	assert.False(t, c.Cancel)
	assert.True(t, c.Canceling)

	// The page of the run hides the card, by run id or by task id.
	snap := store.Snapshot{Runs: []store.Run{run}, Tasks: []store.Task{task}}
	assert.Empty(t, runDockOf(snap, runAt(25), "run", run.ID).Cards)
	assert.Empty(t, runDockOf(snap, runAt(25), "task", "t1").Cards)
	assert.Len(t, runDockOf(snap, runAt(25), "run", "other", "task", "other").Cards, 1)
}

func TestRunDockTaskEnded(t *testing.T) {
	failed := runFixture(t, "create-failed.jsonl", -1)[0]
	task := store.Task{ID: "t", Display: "d", Trace: failed.Trace, Start: runAt(0), End: failed.End, Status: store.TaskFailed, Err: "node worker-1: timed out", Output: "tail"}
	c := runDockOf(store.Snapshot{Runs: []store.Run{failed}, Tasks: []store.Task{task}}, runAt(900)).Cards[0]
	assert.Equal(t, runFailed, c.Class)
	assert.Equal(t, "✗", c.Mark)
	assert.Equal(t, "Show what failed", c.Action)
	assert.Equal(t, "failed in 5m16s", c.Line)
	assert.Equal(t, "5m16s", c.Elapsed)
	assert.Zero(t, c.Since)
	assert.True(t, c.Dismiss)
	assert.False(t, c.Cancel)
	assert.Equal(t, "node worker-1: timed out", c.Err)

	// The command ended, and its task not yet: the run says how.
	task.Status, task.End = store.TaskRunning, nil
	c = runDockOf(store.Snapshot{Runs: []store.Run{failed}, Tasks: []store.Task{task}}, runAt(900)).Cards[0]
	assert.Equal(t, runFailed, c.Class)
	assert.False(t, c.Dismiss, "it can be stopped, not closed")

	ok := runFixture(t, "create-ok.jsonl", -1)[0]
	task = store.Task{ID: "t", Trace: ok.Trace, Status: store.TaskOK, Action: "create-cluster"}
	c = runDockOf(store.Snapshot{Runs: []store.Run{ok}, Tasks: []store.Task{task}}, runAt(900)).Cards[0]
	assert.Equal(t, runOK, c.Class)
	assert.Equal(t, "create cluster", c.Title)
	assert.Equal(t, "done in 53s", c.Line)
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
			runRunning, "starting", true, "sind create cluster", true, false, false, "Show progress"},
		"stopping": {store.Task{ID: "a", Action: "power", Start: runAt(90), Status: store.TaskRunning, Canceling: true},
			runRunning, "stopping", true, "power", false, true, false, "Show progress"},
		"no start": {store.Task{ID: "a", Status: store.TaskRunning},
			runRunning, "starting", false, "", true, false, false, "Show progress"},
		"ok": {store.Task{ID: "a", Display: "d", Start: runAt(90), End: runPtr(runAt(95)), Status: store.TaskOK},
			runOK, "done in 5.0s", false, "d", false, false, true, "Show progress"},
		"failed": {store.Task{ID: "a", Display: "d", Start: runAt(90), End: runPtr(runAt(95)), Status: store.TaskFailed},
			runFailed, "failed in 5.0s", false, "d", false, false, true, "Show what failed"},
		"canceled": {store.Task{ID: "a", Display: "d", Start: runAt(90), End: runPtr(runAt(95)), Status: store.TaskCanceled},
			runCanceled, "canceled in 5.0s", false, "d", false, false, true, "Show progress"},
		"odd": {store.Task{ID: "a", Display: "d", Start: runAt(90), End: runPtr(runAt(95)), Status: "weird"},
			runEnded, "ended in 5.0s", false, "d", false, false, true, "Show progress"},
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
		assert.Equal(t, tc.cancel, c.Cancel, name)
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
	assert.Equal(t, "t", d.Cards[0].TaskID)
	c := d.Cards[1]
	assert.Equal(t, "terminal", c.Chip)
	assert.Equal(t, "r:"+run.ID, c.SayKey)
	assert.Empty(t, c.TaskID)
	assert.False(t, c.Cancel)
	assert.False(t, c.Dismiss)
	assert.Equal(t, "/runs/"+run.ID, c.Href)
	assert.Equal(t, "nodes 0/3 · controller ready munge", c.Line)
	assert.Empty(t, runDockOf(snap, runAt(25), "run", run.ID).Cards[1:])
}

func TestRunDockFit(t *testing.T) {
	card := func(class string, id int) dockCard { return dockCard{Class: class, Title: fmt.Sprint(id)} }
	titles := func(d dockData) []string {
		var out []string
		for _, c := range d.Cards {
			out = append(out, c.Title)
		}
		return out
	}
	// Room for all.
	d := dockFit([]dockCard{card(runOK, 1), card(runRunning, 2)})
	assert.Equal(t, []string{"1", "2"}, titles(d))
	assert.Zero(t, d.More)
	// Ended cards go first, oldest first.
	d = dockFit([]dockCard{card(runOK, 1), card(runRunning, 2), card(runFailed, 3), card(runRunning, 4), card(runRunning, 5), card(runRunning, 6)})
	assert.Equal(t, []string{"2", "4", "5", "6"}, titles(d))
	assert.Zero(t, d.More)
	// Running cards stay; the last ones that do not fit are counted.
	var all []dockCard
	for i := range 7 {
		all = append(all, card(runRunning, i))
	}
	d = dockFit(all)
	assert.Equal(t, []string{"0", "1", "2", "3"}, titles(d))
	assert.Equal(t, 3, d.More)
	assert.Empty(t, dockFit(nil).Cards)
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
