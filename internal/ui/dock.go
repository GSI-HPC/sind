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

func init() { registerDock(defaultRegistry) }

// registerDock adds the two fragments of the task dock, "tasks" and
// "tasks-menu" (tasksFragment).
func registerDock(r *Registry) {
	r.AddFragment(tasksFragment("tasks"))
	r.AddFragment(tasksFragment("tasks-menu"))
}

const (
	// dockMax is the most commands that ended that the list of commands
	// shows; Activity has the rest.
	dockMax = 6
	// dockStripFor is how long the strip shows a command that ended, when
	// no other runs: long enough to read how it ended.
	dockStripFor = 15 * time.Second
)

// tasksFragment is a part of the task dock, which every page has: the
// commands that sind ui started, and the runs that other processes write
// to the event log, with their progress. The fragment "tasks" is the strip
// at the bottom of the main column, which shows one command; "tasks-menu"
// is the button of the top bar, which counts the commands, and the list
// that it opens. Both load the same dockData. The page that shows a run or
// a task sets the Params run and task (see runPage and runTaskPage), so
// that the strip does not show what the page shows already.
func tasksFragment(name string) Fragment {
	return Fragment{
		Name:     name,
		Topics:   []store.Topic{store.TopicTasks},
		Prefixes: []string{"run/"},
		Params:   []string{"run", "task"},
		Load:     func(v *View) error { v.Data = dockLoad(v); return nil },
	}
}

// dockData is what the templates "tasks" and "tasks-menu" draw.
type dockData struct {
	// Strip is the command of the strip, nil for none: the one that
	// started last of those that run, one that works before one that waits
	// for the lock of its realm, else the one that ended last, for
	// dockStripFor after its end. It is never the command of the page.
	Strip *dockCard
	// StripMore says what else the list holds, as "1 more running · 3
	// ended", for the button of the strip that opens it.
	StripMore string
	// Running are the commands that go on, the one that started last
	// first. Ended are the tasks that ended and that the user has not
	// cleared, the one that ended last first, at most dockMax; More counts
	// the rest, which Activity lists.
	Running []dockCard
	Ended   []dockCard
	More    int
	// EndedLabel heads the commands that ended: "Ended today", or
	// "Ended" when one ended before today.
	EndedLabel string
	// Parts are what the button of the top bar says, as "1 running" and
	// "1 failed", and Label is its name: the same words, then the other
	// counts, as "Commands: 2 ended: 1 stopped, 1 done".
	Parts []dockPart
	Label string
	// Failed counts the commands of the list that failed.
	Failed int
}

// dockPart is a count of the button of the top bar.
type dockPart struct {
	N    int
	Word string
	// Class is run for the commands that go on, bad for those that
	// failed, and empty for the others.
	Class string
}

// dockCard is a command of the dock.
type dockCard struct {
	// Class is running, ok, failed, canceled or ended.
	Class string
	Mark  string
	// Title is the command with the flags that tell it from others, and
	// Tip the whole command line, as the dialog showed it. Name is Title
	// without the flags, which Flags holds: the templates draw them apart,
	// so that a narrow strip or list leaves the flags out, not the name.
	Title string
	Name  string
	Flags string
	Tip   string
	// Realm is the realm of a command of sind ui.
	Realm string
	// Chip says where a run came from that sind ui did not start.
	Chip    string
	Segs    runBar
	Line    string
	Elapsed string
	// Result says what came of a command that ended (runResult), and
	// ResultClass colours it: bad for a failure, warn for a stop.
	Result      string
	ResultClass string
	// When is the time of day that a command ended, with the date for
	// one before today.
	When string
	// Since is what run.js counts the clock from; 0 for a clock that
	// stands still.
	Since int64
	// HideAt is when run.js takes a command that ended off the strip, in
	// milliseconds since 1970; 0 for one that runs.
	HideAt int64
	// Href is the run page, or the task page for a task whose run the log
	// does not hold yet.
	Href   string
	Action string
	// Open leads to the cluster that a create made, once it ended ok.
	Open *runLink
	// TaskID is the task of the card; empty for a terminal run. Dismiss
	// is set for a task that ended, which × clears, with the token CSRF.
	TaskID  string
	Dismiss bool
	CSRF    string
	// Stop is the Stop button of a task that runs.
	Stop *runStop
	// Canceling is set while a stop is under way.
	Canceling bool
	// SayKey, SayState and Say are what run.js announces: the sentence
	// Say, once, when the state of the command with the key changes.
	SayKey   string
	SayState string
	Say      string

	// waiting is set while the command waits for the lock of its realm;
	// start and end order the commands.
	waiting    bool
	start, end time.Time
	// here is set for the command of what the page shows already, which
	// the strip leaves out.
	here bool
}

// dockLoad builds the commands: one for each task that the user has not
// cleared, then one for each run that goes on without a task.
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
		cards[i].CSRF = v.CSRF
		if cards[i].Stop != nil {
			cards[i].Stop.CSRF = v.CSRF
		}
	}
	return dockFit(cards, v.Now)
}

// dockWords name the counts of the commands that ended, by class.
var dockWords = map[string]string{runFailed: "failed", runCanceled: "stopped", runOK: "done", runEnded: "ended"}

// dockFit sorts the commands into those that run, the one that started
// last first, and those that ended, the one that ended last first, picks
// the command of the strip and counts them for the button of the top bar.
// The button says how many run and how many failed, or with neither, how
// many ended; it counts only what the list shows.
func dockFit(cards []dockCard, now time.Time) dockData {
	var d dockData
	for _, c := range cards {
		if c.Class == runRunning {
			d.Running = append(d.Running, c)
		} else {
			d.Ended = append(d.Ended, c)
		}
	}
	slices.SortStableFunc(d.Running, func(a, b dockCard) int { return b.start.Compare(a.start) })
	slices.SortStableFunc(d.Ended, func(a, b dockCard) int { return b.end.Compare(a.end) })
	d.More = max(len(d.Ended)-dockMax, 0)
	d.Ended = d.Ended[:len(d.Ended)-d.More]
	d.EndedLabel = "Ended today"
	counts := map[string]int{}
	for _, c := range d.Ended {
		counts[c.Class]++
		if !runSameDay(c.end, now) {
			d.EndedLabel = "Ended"
		}
	}
	d.Failed = counts[runFailed]
	d.Strip = dockStrip(d, now)

	if n := len(d.Running); n > 0 {
		d.Parts = append(d.Parts, dockPart{N: n, Word: "running", Class: "run"})
	}
	if d.Failed > 0 {
		d.Parts = append(d.Parts, dockPart{N: d.Failed, Word: "failed", Class: "bad"})
	}
	var rest []string
	for _, class := range []string{runFailed, runCanceled, runOK, runEnded} {
		if n := counts[class]; n > 0 && (class != runFailed || d.Parts == nil) {
			rest = append(rest, fmt.Sprintf("%d %s", n, dockWords[class]))
		}
	}
	sep := ", "
	if d.Parts == nil && len(d.Ended) > 0 {
		d.Parts, sep = []dockPart{{N: len(d.Ended), Word: "ended"}}, ": "
	}
	if d.Parts != nil {
		var shown []string
		for _, p := range d.Parts {
			shown = append(shown, fmt.Sprintf("%d %s", p.N, p.Word))
		}
		d.Label = "Commands: " + strings.Join(shown, ", ")
		if len(rest) > 0 {
			d.Label += sep + strings.Join(rest, ", ")
		}
	}
	d.StripMore = dockStripMore(d)
	return d
}

// dockStrip picks the command of the strip: the one that started last of
// those that run and do not wait for the lock of their realm, else of those
// that wait, else the one that ended last, while it ended less than
// dockStripFor ago. It leaves out the command of the page.
func dockStrip(d dockData, now time.Time) *dockCard {
	for _, waiting := range []bool{false, true} {
		for _, c := range d.Running {
			if !c.here && c.waiting == waiting {
				return &c
			}
		}
	}
	for _, c := range d.Ended {
		if c.here || c.end.IsZero() {
			continue
		}
		hide := c.end.Add(dockStripFor)
		if !now.Before(hide) {
			return nil
		}
		c.HideAt = hide.UnixMilli()
		return &c
	}
	return nil
}

// dockStripMore says what the list holds beside the command of the strip:
// "1 more running · 3 ended". It is empty without a strip.
func dockStripMore(d dockData) string {
	if d.Strip == nil {
		return ""
	}
	running, ended := len(d.Running), len(d.Ended)
	if d.Strip.Class == runRunning {
		running--
	} else {
		ended--
	}
	var parts []string
	if running > 0 {
		parts = append(parts, fmt.Sprintf("%d more running", running))
	}
	if ended > 0 {
		word := "ended"
		if d.Strip.Class != runRunning {
			word = "more ended"
		}
		parts = append(parts, fmt.Sprintf("%d %s", ended, word))
	}
	return strings.Join(parts, " · ")
}

// dockTaskCard draws a task, with its run if the log holds one (not nil).
func dockTaskCard(t store.Task, run *store.Run, now time.Time) dockCard {
	c := dockCard{TaskID: t.ID, Tip: t.Display, Realm: t.Realm, Href: "/tasks/" + t.ID, start: t.Start, Title: taskTitle(t, run)}
	c.Name, c.Flags = taskTitleParts(t, run)
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
	why := t.Err
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
		if c.end.IsZero() && run.End != nil {
			c.end = *run.End
		}
		if why == "" {
			why = runFailure(*run)
		}
		if run.Done() {
			c.Result, c.ResultClass = runResult(*run, now)
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
		case c.Class != runRunning:
			c.Line = dockWords[c.Class] + " in " + c.Elapsed
		}
	}
	if c.Class != runRunning {
		c.dockEnded(why, now)
	}
	switch c.Class {
	case runRunning:
		c.Action = "Show progress"
	case runFailed:
		c.Action = "Show what failed"
	default:
		c.Action = "Details"
	}
	if c.Class == runOK && t.Action == "create-cluster" && t.Cluster != "" {
		c.Open = &runLink{Label: "Open " + t.Cluster, Href: pgClusterURL(t.Realm, t.Cluster, ""), Primary: true}
	}
	c.Mark = runMark(c.Class)
	if c.Class == runRunning {
		c.Stop = newRunStop(t, run, c.Title, "", true)
	}
	c.Canceling = c.Class == runRunning && t.Canceling
	c.Dismiss = taskEnded(t)
	c.SayKey, c.SayState = "t:"+t.ID, runSayState(c.Class, t.Canceling)
	c.Say = runSay(c.Title, c.SayState, c.Elapsed, why)
	return c
}

// dockEnded says what came of a command that ended and when: the result
// of its run, else the first line of the error of a failure, else how it
// ended, as "done in 5.0s".
func (c *dockCard) dockEnded(why string, now time.Time) {
	if !c.end.IsZero() {
		c.When = strings.TrimPrefix(runWhen(c.end, now), "today ")
	}
	if c.Result != "" {
		return
	}
	c.Result = c.Line
	switch first, _, _ := strings.Cut(strings.TrimSpace(why), "\n"); {
	case c.Class == runFailed && first != "":
		c.Result, c.ResultClass = first, "bad"
	case c.Class == runFailed:
		c.ResultClass = "bad"
	case c.Class == runCanceled:
		c.ResultClass = "warn"
	}
}

// dockRunCard draws a run that a process other than this sind ui writes,
// with the chip that says where it came from.
func dockRunCard(run store.Run, chip string, now time.Time) dockCard {
	return dockCard{
		Class: runRunning, Mark: runMark(runRunning), Title: run.Title, Name: run.Title, Chip: chip,
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
// which only the whole command needs, is left out. A restart of a service
// says what it restarts: "restart slurmd worker-4.dev".
func taskTitle(t store.Task, run *store.Run) string {
	name, flags := taskTitleParts(t, run)
	return strings.TrimSpace(name + " " + flags)
}

// taskTitleParts is the title of a task's command (taskTitle) in two
// parts: the command with its cluster, and the flags.
func taskTitleParts(t store.Task, run *store.Run) (name, flags string) {
	argv := t.Argv
	if len(argv) == 0 {
		argv = strings.Fields(t.Display)
	}
	words, fl := taskWords(argv)
	if unit := pgFormValue(t.Form, "unit"); t.Action == "restart-service" && unit != "" && len(words) == 2 {
		// sind ssh worker-4.dev -- systemctl restart slurmd
		return "restart " + unit + " " + words[1], ""
	}
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
	return base, strings.Join(fl, " ")
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
