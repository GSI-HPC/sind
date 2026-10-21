// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

func init() { defaultRegistry.AddFragment(tasksFragment()) }

// dockMax is the most cards the dock shows at once.
const dockMax = 4

// tasksFragment is the task dock at the bottom right of every page: the
// commands that sind ui started, and the runs that other processes write to
// the event log, with their progress. The page that shows a run or a task
// sets the Params run and task (see runPage and runTaskPage), so that the
// dock does not draw the card of what the page shows already.
func tasksFragment() Fragment {
	return Fragment{
		Name:     "tasks",
		Topics:   []store.Topic{store.TopicTasks},
		Prefixes: []string{"run/"},
		Params:   []string{"run", "task"},
		Shell:    true,
		Load:     func(v *View) error { v.Data = dockLoad(v); return nil },
	}
}

// dockData is what the template "tasks" draws.
type dockData struct {
	Cards []dockCard
	// More counts the cards that did not fit.
	More int
}

// dockCard is a card of the dock.
type dockCard struct {
	// Class is running, ok, failed, canceled or ended.
	Class string
	Mark  string
	Title string
	// Terminal marks a run of a process other than sind ui.
	Terminal bool
	Segs     []runSeg
	Line     string
	Elapsed  string
	// Since is what run.js counts the clock from; 0 for a clock that
	// stands still.
	Since int64
	// Href is the run page, or the task page for a task whose run the log
	// does not hold yet.
	Href   string
	Action string
	// TaskID is the task of the card; empty for a terminal run.
	TaskID  string
	Dismiss bool
	Cancel  bool
	// Canceling is set while a stop is under way.
	Canceling bool
	Err       string
	Output    string
}

// dockLoad builds the cards: a card for each task, then one for each run
// that goes on without a task.
func dockLoad(v *View) dockData {
	hideRun, hideTask := v.Params.Get("run"), v.Params.Get("task")
	var cards []dockCard
	for _, t := range v.Snap.Tasks {
		run, hasRun := v.Snap.RunOfTrace(t.Trace)
		if (hideTask != "" && t.ID == hideTask) || (hasRun && hideRun != "" && run.ID == hideRun) {
			continue
		}
		cards = append(cards, dockTaskCard(t, run, hasRun, v.Now))
	}
	for _, run := range v.Snap.Runs {
		if run.Done() || (hideRun != "" && run.ID == hideRun) {
			continue
		}
		if _, ok := runTaskOf(v.Snap, run.Trace); ok {
			continue
		}
		cards = append(cards, dockRunCard(run, v.Now))
	}
	return dockFit(cards)
}

// dockFit keeps at most dockMax cards. The cards of commands that have
// ended go first, oldest first, as the user can close them; then the last
// cards are left out and counted.
func dockFit(cards []dockCard) dockData {
	for i := 0; i < len(cards) && len(cards) > dockMax; {
		if cards[i].Class != runRunning {
			cards = append(cards[:i], cards[i+1:]...)
			continue
		}
		i++
	}
	more := max(len(cards)-dockMax, 0)
	return dockData{Cards: cards[:len(cards)-more], More: more}
}

// dockTaskCard draws a task, with its run if the log holds one.
func dockTaskCard(t store.Task, run store.Run, hasRun bool, now time.Time) dockCard {
	c := dockCard{TaskID: t.ID, Title: t.Display, Href: "/tasks/" + t.ID, Err: t.Err, Output: t.Output}
	if c.Title == "" {
		c.Title = t.Action
	}
	switch t.Status {
	case store.TaskOK:
		c.Class = runOK
	case store.TaskFailed:
		c.Class = runFailed
	case store.TaskCanceled:
		c.Class = runCanceled
	default:
		c.Class = runRunning
		if t.Done() {
			c.Class = runEnded
		}
	}
	c.Action = "Show progress"
	if hasRun {
		c.Title, c.Href = runTitle(run, &t), "/runs/"+run.ID
		if c.Class == runRunning && run.Done() {
			c.Class = runStatusClass(run)
		}
		c.Segs, c.Line = runSegs(run), runLine(run, &t, now)
		c.Elapsed = runDur(runElapsed(run, now))
		if c.Class == runRunning {
			c.Since = runSince(run)
		}
	} else {
		c.Segs = []runSeg{{Class: c.Class, Title: c.Class}}
		c.Elapsed = runDur(taskElapsed(t, now))
		c.Line = "starting"
		if c.Class == runRunning && !t.Start.IsZero() {
			c.Since = t.Start.UnixMilli()
		}
		switch {
		case c.Class == runRunning && t.Canceling:
			c.Line = "stopping"
		case c.Class == runOK:
			c.Line = "done in " + c.Elapsed
		case c.Class != runRunning:
			c.Line = c.Class + " in " + c.Elapsed
		}
	}
	if c.Class == runFailed {
		c.Action = "Show what failed"
	}
	c.Mark = runMark(c.Class)
	c.Cancel = c.Class == runRunning && !t.Canceling
	c.Canceling = c.Class == runRunning && t.Canceling
	c.Dismiss = taskEnded(t)
	return c
}

// dockRunCard draws a run that a process other than sind ui writes.
func dockRunCard(run store.Run, now time.Time) dockCard {
	return dockCard{
		Class: runRunning, Mark: runMark(runRunning), Title: run.Title, Terminal: true,
		Segs: runSegs(run), Line: runLine(run, nil, now),
		Elapsed: runDur(runElapsed(run, now)), Since: runSince(run),
		Href: "/runs/" + run.ID, Action: "Show progress",
	}
}

// taskEnded reports whether a task is over, so that the user may close its
// card: it has an end, or a status other than running.
func taskEnded(t store.Task) bool { return t.Done() || t.Status != store.TaskRunning }

// taskElapsed is how long a task has run, 0 for one without a start.
func taskElapsed(t store.Task, now time.Time) time.Duration {
	if t.Start.IsZero() {
		return 0
	}
	end := now
	if t.End != nil {
		end = *t.End
	}
	return max(end.Sub(t.Start), 0)
}
