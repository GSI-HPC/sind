// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

func init() { defaultRegistry.AddFragment(tasksFragment()) }

const (
	// dockMax is the most cards the dock draws; it shows one of them, or
	// all when the user opens it.
	dockMax = 6
	// dockHideAfter is how long the card of a command that ended ok stays
	// before it hides: the summary bar still counts it, and Activity
	// lists it. A failed or stopped command stays until it is cleared.
	dockHideAfter = 8 * time.Second
)

// tasksFragment is the task dock at the bottom right of every page: the
// commands that sind ui started, and the runs that other processes write to
// the event log, with their progress. The page that shows a run or a task
// sets the Params run and task (see runPage and runTaskPage), so that the
// dock does not draw the card of what the page shows already; its bar
// counts it.
func tasksFragment() Fragment {
	return Fragment{
		Name:     "tasks",
		Topics:   []store.Topic{store.TopicTasks},
		Prefixes: []string{"run/"},
		Params:   []string{"run", "task"},
		Load:     func(v *View) error { v.Data = dockLoad(v); return nil },
	}
}

// dockData is what the template "tasks" draws: a summary bar when there is
// more than one command, and the cards, the one that matters most first.
// The dock shows the first card only, until the user opens it.
type dockData struct {
	Cards []dockCard
	// More counts the cards that did not fit.
	More int
	// Bar is set when the summary bar shows: with a card to show and more
	// than one command. Parts are what it says, such as "2 running".
	Bar   bool
	Parts []dockPart
	// Running is set while a command goes on, for the spinner of the bar.
	Running bool
	// Clear is set when a command has ended, which Clear done removes.
	Clear bool
	// Toggle is set when there is more than one card to show.
	Toggle bool
}

// dockPart is a count of the summary bar.
type dockPart struct {
	Text string
	// Strong marks the count that matters most: the commands that run.
	Strong bool
}

// dockCard is a card of the dock.
type dockCard struct {
	// Class is running, ok, failed, canceled or ended.
	Class string
	Mark  string
	// Title is the command with the flags that tell it from others, and
	// Tip the whole command line, as the dialog showed it.
	Title string
	Tip   string
	// Realm is the realm of a command of sind ui.
	Realm string
	// Chip says where a run came from that sind ui did not start.
	Chip    string
	Segs    runBar
	Line    string
	Elapsed string
	// Since is what run.js counts the clock from; 0 for a clock that
	// stands still.
	Since int64
	// HideAt is when run.js hides the card of a command that ended ok, in
	// milliseconds since 1970; 0 for a card that stays.
	HideAt int64
	// Href is the run page, or the task page for a task whose run the log
	// does not hold yet.
	Href   string
	Action string
	// TaskID is the task of the card; empty for a terminal run.
	TaskID  string
	Dismiss bool
	// Stop is the Stop button of a task that runs.
	Stop *runStop
	// Canceling is set while a stop is under way.
	Canceling bool
	// Compact is set for a command that failed or was stopped: its card
	// keeps to the title, one line and its buttons, as it stays until the
	// user hides it, and leaves the output to the run page.
	Compact bool
	// Why is the first line of the error of a command that failed.
	Why string
	// SayKey, SayState and Say are what run.js announces: the sentence
	// Say, once, when the state of the command with the key changes.
	SayKey   string
	SayState string
	Say      string

	// waiting, start and end order the cards: waiting is set while the
	// command waits for the lock of its realm.
	waiting    bool
	start, end time.Time
	// here is set for the card of what the page shows already, which the
	// bar counts and the dock does not draw.
	here bool
}

// dockLoad builds the cards: a card for each task whose card the user has
// not closed, then one for each run that goes on without a task.
func dockLoad(v *View) dockData {
	hideRun, hideTask := v.Params.Get("run"), v.Params.Get("task")
	var cards []dockCard
	for _, t := range v.Snap.Tasks {
		if t.Hidden {
			continue
		}
		var run *store.Run
		if r, ok := v.Snap.RunOfTrace(t.Trace); ok {
			run = &r
		}
		c := dockTaskCard(t, run, v.Now)
		c.here = (hideTask != "" && t.ID == hideTask) || (run != nil && hideRun != "" && run.ID == hideRun)
		cards = append(cards, c)
	}
	for _, run := range v.Snap.Runs {
		if run.Done() {
			continue
		}
		if _, ok := runTaskOf(v.Snap, run.Trace); ok {
			continue
		}
		c := dockRunCard(run, runSourceChip(runSource(v.Snap, run)), v.Now)
		c.here = hideRun != "" && run.ID == hideRun
		cards = append(cards, c)
	}
	for i := range cards {
		if cards[i].Stop != nil {
			cards[i].Stop.CSRF = v.CSRF
		}
	}
	return dockFit(cards, v.Now)
}

// dockRank orders the cards: the commands that work, then those that wait
// for the lock of their realm, then those that failed or were stopped, then
// those that ended ok.
func dockRank(c dockCard) int {
	switch {
	case c.Class == runRunning && c.waiting:
		return 1
	case c.Class == runRunning:
		return 0
	case c.Class == runOK:
		return 3
	}
	return 2
}

// dockWaiting is the key of the bar's count of the commands that wait for
// the lock of their realm: they have not started their work yet.
const dockWaiting = "waiting"

// dockFit sums the cards up for the bar, with the commands that wait for
// the lock of their realm apart from those that run, as "1 running · 1
// waiting". It leaves out the card of what the page shows and the cards of
// commands that ended ok a while ago, and orders the rest (dockRank): the
// commands that run oldest first, those that ended newest first. At most
// dockMax are drawn, and the rest counted.
func dockFit(cards []dockCard, now time.Time) dockData {
	var d dockData
	counts := map[string]int{}
	var shown []dockCard
	for _, c := range cards {
		key := c.Class
		if c.Class == runRunning && c.waiting {
			key = dockWaiting
		}
		counts[key]++
		d.Clear = d.Clear || c.Dismiss
		if c.here || (c.HideAt > 0 && now.UnixMilli() >= c.HideAt) {
			continue
		}
		shown = append(shown, c)
	}
	slices.SortStableFunc(shown, func(a, b dockCard) int {
		if r := cmp.Compare(dockRank(a), dockRank(b)); r != 0 {
			return r
		}
		if a.Class == runRunning {
			return a.start.Compare(b.start)
		}
		return b.end.Compare(a.end)
	})
	d.More = max(len(shown)-dockMax, 0)
	d.Cards = shown[:len(shown)-d.More]
	d.Running = counts[runRunning]+counts[dockWaiting] > 0
	d.Bar = len(shown) > 0 && len(cards) > 1
	d.Toggle = len(shown) > 1
	for _, p := range []struct {
		class, label string
	}{{runRunning, "running"}, {dockWaiting, "waiting"}, {runFailed, "failed"}, {runCanceled, "stopped"}, {runEnded, "ended"}, {runOK, "done"}} {
		if n := counts[p.class]; n > 0 {
			d.Parts = append(d.Parts, dockPart{Text: fmt.Sprintf("%d %s", n, p.label), Strong: p.class == runRunning})
		}
	}
	return d
}

// dockTaskCard draws a task, with its run if the log holds one (not nil).
func dockTaskCard(t store.Task, run *store.Run, now time.Time) dockCard {
	c := dockCard{TaskID: t.ID, Tip: t.Display, Realm: t.Realm, Href: "/tasks/" + t.ID, start: t.Start, Title: taskTitle(t, run)}
	if t.End != nil {
		c.end = *t.End
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
	if run != nil {
		c.Href = "/runs/" + run.ID
		if c.Class == runRunning && run.Done() {
			c.Class = runStatusClass(*run)
		}
		shown := runRevealed(*run, now)
		c.Segs, c.Line = runSegs(shown), runLine(shown, &t, now)
		c.Elapsed, c.waiting = runDur(runElapsed(*run, now)), runWaitsForLock(*run)
		if c.Class == runRunning {
			c.Since = runSince(*run)
		}
	} else {
		c.Segs = runBar{{Class: c.Class, Title: c.Class}}
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
	switch c.Class {
	case runRunning:
		c.Action = "Show progress"
	case runFailed:
		c.Action = "Show what failed"
	default:
		c.Action = "Details"
	}
	if c.Class == runOK && t.End != nil {
		c.HideAt = t.End.Add(dockHideAfter).UnixMilli()
	}
	c.Mark = runMark(c.Class)
	if c.Class == runRunning {
		c.Stop = newRunStop(t, run, c.Title, "", true)
	}
	c.Canceling = c.Class == runRunning && t.Canceling
	c.Dismiss = taskEnded(t)
	why := t.Err
	if run != nil && why == "" {
		why = runFailure(*run)
	}
	c.Compact = c.Class == runFailed || c.Class == runCanceled || c.Class == runEnded
	if c.Class == runFailed {
		c.Why, _, _ = strings.Cut(strings.TrimSpace(why), "\n")
	}
	c.SayKey, c.SayState = "t:"+t.ID, runSayState(c.Class, t.Canceling)
	c.Say = runSay(c.Title, c.SayState, c.Elapsed, why)
	return c
}

// dockRunCard draws a run that a process other than this sind ui writes,
// with the chip that says where it came from.
func dockRunCard(run store.Run, chip string, now time.Time) dockCard {
	return dockCard{
		Class: runRunning, Mark: runMark(runRunning), Title: run.Title, Chip: chip,
		Segs: runSegs(runRevealed(run, now)), Line: runLine(runRevealed(run, now), nil, now),
		Elapsed: runDur(runElapsed(run, now)), Since: runSince(run),
		Href: "/runs/" + run.ID, Action: "Show progress",
		SayKey: "r:" + run.ID, SayState: runRunning, Say: runSay(run.Title, runRunning, "", ""),
		start: run.Start, waiting: runWaitsForLock(run),
	}
}

// taskTitle is the title of a task's command: the command as the log names
// it, with the cluster, or else the words of its command line, followed by
// the flags that tell it from others, as in "create worker dev --count 3
// --cpus 4". The realm has a chip of its own, and the path of --config,
// which only the whole command needs, is left out.
func taskTitle(t store.Task, run *store.Run) string {
	argv := t.Argv
	if len(argv) == 0 {
		argv = strings.Fields(t.Display)
	}
	words, flags := taskWords(argv)
	base := strings.Join(words, " ")
	switch {
	case run != nil && run.Title != "":
		base = runTitle(*run, &t)
	case t.Cluster != "" && !slices.ContainsFunc(words, func(w string) bool { return w == t.Cluster || strings.HasSuffix(w, "."+t.Cluster) }):
		base = strings.TrimSpace(base + " " + t.Cluster)
	}
	if base == "" {
		base = cmp.Or(t.Action, t.Display)
	}
	return strings.TrimSpace(base + " " + strings.Join(flags, " "))
}

// taskWords splits a sind command line, argv[0] left out, into its command
// words with their arguments, and the flags from the first one on, with
// their values. --realm and --config are left out, with their values.
func taskWords(argv []string) (words, flags []string) {
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--realm" || a == "--config":
			i++
		case strings.HasPrefix(a, "--realm=") || strings.HasPrefix(a, "--config="):
		case strings.HasPrefix(a, "-") || len(flags) > 0:
			flags = append(flags, a)
		default:
			words = append(words, a)
		}
	}
	return words, flags
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
