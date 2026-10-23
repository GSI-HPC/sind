// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/activity"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

func init() { registerRun(defaultRegistry) }

// runFragments are the live parts of the run page, in the order the page
// places them. Each depends on the one run that the page shows.
var runFragments = []string{
	"run-head", "run-summary", "run-failed", "run-timeline", "run-nodes", "run-more", "run-facts",
}

// registerRun adds the progress display to r: the run page, the Activity
// page, the page of a task whose run the log does not hold yet, and the
// routes of the task dock. The dock itself is the shell fragment "tasks".
func registerRun(r *Registry) {
	loads := map[string]func(rc runCtx) any{
		"run-head":     func(rc runCtx) any { return runHeadOf(rc) },
		"run-summary":  func(rc runCtx) any { return runSummaryOf(rc) },
		"run-failed":   func(rc runCtx) any { return runFailedOf(rc) },
		"run-timeline": func(rc runCtx) any { return runTimelineOf(rc) },
		"run-nodes":    func(rc runCtx) any { return runNodesOf(rc) },
		"run-more":     func(rc runCtx) any { return runMoreOf(rc) },
		"run-facts":    func(rc runCtx) any { return runFactsOf(rc) },
	}
	for _, name := range runFragments {
		build := loads[name]
		r.AddFragment(Fragment{
			Name:   name,
			Params: []string{"id"},
			Watch:  func(p Params) []store.Topic { return []store.Topic{store.RunTopic(p.Get("id"))} },
			Load: func(v *View) error {
				rc := newRunCtx(v)
				v.Data = nil
				if !rc.Found {
					// The run was dropped while the page is open: the head
					// says so, the other parts clear.
					if name == "run-head" {
						v.Data = &runHead{Gone: true}
					}
					return nil
				}
				v.Data = build(rc)
				return nil
			},
		})
	}
	r.AddPage(Page{
		Name: "run", Pattern: "GET /runs/{id}", Title: "Run", Nav: "activity",
		Fragments: runFragments, Load: runPageLoad,
	})
	registerRunActivity(r)
	registerRunTask(r)
}

// runCtx is the run that a fragment shows and what goes with it.
type runCtx struct {
	Run   store.Run
	Found bool
	// Task is the task that started the run, nil for a run from a terminal.
	Task *store.Task
	Snap store.Snapshot
	Now  time.Time
}

// newRunCtx finds the run named by the Param id of v.
func newRunCtx(v *View) runCtx {
	rc := runCtx{Snap: v.Snap, Now: v.Now}
	rc.Run, rc.Found = v.Snap.Run(v.Params.Get("id"))
	if t, ok := runTaskOf(v.Snap, rc.Run.Trace); rc.Found && ok {
		// The page names the run as the dock and Activity do, with the
		// flags that tell it from other runs of the command.
		rc.Task = &t
		rc.Run.Title = taskTitle(t, &rc.Run)
	}
	return rc
}

// runPageLoad checks the run exists and tells the task dock not to draw it.
func runPageLoad(v *View) error {
	id := v.Params.Get("id")
	run, ok := v.Snap.Run(id)
	if !ok {
		m := &Miss{
			What: fmt.Sprintf("The activity log holds no run %s.", id),
			Hint: "Activity lists the runs that the log holds. sind keeps the log and one older copy.",
		}
		if _, ok := v.Snap.Task(id); ok {
			m.Links = append(m.Links, MissLink{Label: "Task " + id, URL: "/tasks/" + url.PathEscape(id)})
		}
		m.Links = append(m.Links, MissLink{Label: "Activity", URL: "/activity"}, missOverview)
		return m
	}
	v.Title = run.Title
	if t, ok := runTaskOf(v.Snap, run.Trace); ok {
		v.Title = taskTitle(t, &run)
	}
	if v.Title == "" {
		v.Title = "Run " + id
	}
	v.Params.Set("run", id)
	return nil
}

// runWhen writes a time of day, with the date for a time before today.
func runWhen(t, now time.Time) string {
	if t.IsZero() {
		return "–"
	}
	t, now = t.Local(), now.Local()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04:05")
	}
	return t.Format("Jan 2 15:04")
}

// runHead is the head of the run page.
type runHead struct {
	// Gone is set when the log no longer holds the run.
	Gone      bool
	ID        string
	Title     string
	Pill      string
	PillClass string
	// Terminal is set for a run that this sind ui did not start, which
	// it cannot stop; NoStop says why, while the run goes on.
	Terminal bool
	NoStop   string
	Origin   string
	Program  string
	Started  string
	Exit     string
	Trace    string
	TraceTip string
	// Cluster is the cluster the run's task worked on, when it exists.
	Cluster     string
	ClusterHref string
	// Running is set while the run goes on.
	Running bool
	// TaskID is the task to stop; empty when sind ui cannot stop the run.
	TaskID    string
	Canceling bool
	Suspended bool
}

// runHeadOf builds the head of the run page.
func runHeadOf(rc runCtx) *runHead {
	run := rc.Run
	h := &runHead{
		ID: run.ID, Title: run.Title, Terminal: rc.Task == nil, Origin: runSourceText(runSource(rc.Snap, run)),
		Started: runWhen(run.Start, rc.Now), Trace: run.Trace, TraceTip: run.Trace,
		Running: !run.Done(), Suspended: run.Suspended,
	}
	h.Pill, h.PillClass = runPill(run, rc.Task)
	if len(h.Trace) > 8 {
		h.Trace = h.Trace[:8]
	}
	if run.Program != "" {
		h.Program = strings.TrimSpace(run.Program + " " + run.Version)
	}
	if run.Root.Exit != nil {
		h.Exit = "exit " + strconv.Itoa(*run.Root.Exit)
	}
	if h.Terminal && h.Running {
		switch runSource(rc.Snap, run) {
		case runFromUI:
			h.NoStop = "Another sind ui started this run, so this one cannot stop it. Stop it there."
		case runFromProgram:
			h.NoStop = "Another program started this run, so sind ui cannot stop it. Stop it in that program."
		default:
			h.NoStop = "This run was started from a terminal, so sind ui cannot stop it. Press Ctrl-C in that terminal to stop it."
		}
	}
	if t := rc.Task; t != nil {
		if h.Running {
			h.TaskID, h.Canceling = t.ID, t.Canceling
		}
		if t.Cluster != "" && runHasCluster(rc.Snap, t.Realm, t.Cluster) {
			h.Cluster = t.Cluster
			h.ClusterHref = "/clusters/" + t.Realm + "/" + t.Cluster
		}
	}
	return h
}

// runHasCluster reports whether the snapshot holds the cluster.
func runHasCluster(snap store.Snapshot, realm, name string) bool {
	for _, c := range snap.Clusters[realm] {
		if c.Name == name {
			return true
		}
	}
	return false
}

// runCount is a number of the summary.
type runCount struct {
	Label string
	Class string
	N     int
}

// runSummary is the panel with the clock, the bar and the counts.
type runSummary struct {
	// Title names the run, for the name of its bar.
	Title   string
	Elapsed string
	Since   int64
	Segs    runBar
	Line    string
	Counts  []runCount
	// NoTargets is set for a run without targets, which has nothing to count.
	NoTargets bool
	Hints     []string
	// DocTitle is what run.js puts in the browser tab.
	DocTitle string
	// SayKey, SayState and Say are what run.js announces when the state
	// of the run changes, as for a card of the dock; SaySince is when the
	// run started, in milliseconds since 1970, so that a page opened just
	// after the start announces it.
	SayKey   string
	SayState string
	Say      string
	SaySince int64
}

// runSummaryOf builds the summary.
func runSummaryOf(rc runCtx) *runSummary {
	run := rc.Run
	s := &runSummary{
		Title: run.Title, Elapsed: runDur(runElapsed(run, rc.Now)), Since: runSince(run),
		Segs: runSegs(run), Line: runLine(run, rc.Task, rc.Now),
	}
	canceling := false
	s.SayKey = "r:" + run.ID
	if t := rc.Task; t != nil {
		canceling, s.SayKey = t.Canceling, "t:"+t.ID
	}
	s.SayState = runSayState(runStatusClass(run), canceling)
	s.Say = runSay(run.Title, s.SayState, s.Elapsed, runFailure(run))
	if origin := runOrigin(run); !origin.IsZero() {
		s.SaySince = origin.UnixMilli()
	}
	c := activity.CountTargets(run.Root)
	s.NoTargets = c.Total == 0
	s.Counts = []runCount{
		{"ok", "ok", c.OK}, {"running", "run", c.Running}, {"queued", "", c.Queued},
		{"failed", "bad", c.Failed}, {"canceled", "warn", c.Canceled},
	}
	if c.Skipped > 0 {
		s.Counts = append(s.Counts, runCount{"skipped", "", c.Skipped})
	}
	if !run.Done() {
		s.Hints = runWaitHints(run, rc.Now)
	}
	s.DocTitle = runDocTitle(run)
	return s
}

// runWaitHints says what the run waits for: the lock or other waits of the
// command, and how much of their limit the waits of the targets have used.
func runWaitHints(run store.Run, now time.Time) []string {
	var hints []string
	for _, w := range activity.VisibleChildren(run.Root) {
		if w.Kind == activity.KindWait && !w.Done() {
			hint := "Waiting for " + w.Name
			if w.Message != "" {
				hint += ": " + w.Message
			}
			hints = append(hints, hint+".")
		}
	}
	n, worst := 0, 0.0
	var limit time.Duration
	runVisit(run.Root, func(w store.Span, _ string) {
		if w.Kind != activity.KindWait || w.Done() || w.Parent == run.Root.ID {
			return
		}
		used, ok := activity.WaitUsed(w, now)
		if !ok {
			return
		}
		if n == 0 || used > worst {
			worst, limit = used, w.Timeout
		}
		n++
	})
	if n > 0 {
		noun := "waits"
		if n == 1 {
			noun = "wait"
		}
		hints = append(hints, fmt.Sprintf("%d %s for a node to become ready. The slowest has used %d %% of its limit (%s).",
			n, noun, runPct(worst, 1), runDur(limit)))
	}
	return hints
}

// runDocTitle is the title of the browser tab: the step that goes on while
// the run does, so that the progress shows in another tab.
func runDocTitle(run store.Run) string {
	if !run.Done() {
		doing := "starting"
		for _, s := range activity.VisibleChildren(run.Root) {
			if s.Kind == activity.KindStep && s.State == activity.StateRunning && !s.Done() {
				doing = s.Name
				if done, total := runStepCounts(s); total > 0 {
					doing += fmt.Sprintf(" %d/%d", done, total)
				}
				break
			}
		}
		return "▸ " + doing + " · sind ui"
	}
	return runMark(runStatusClass(run)) + " " + run.Title + " · sind ui"
}

// runFailGroup is a set of targets that failed with the same error.
type runFailGroup struct {
	Nodes string
	Class string
	Err   string
}

// runFailPanel is the panel that says why a run failed or stopped.
type runFailPanel struct {
	Title string
	// Class is the callout class of the groups: bad or warn.
	Class  string
	Groups []runFailGroup
	// CmdErr is the error of the command, when no target gave one.
	CmdErr string
	Notes  []string
	// Doctor adds a hint to look at the host.
	Doctor bool
}

// runFailedOf builds the panel; nil for a run that has not failed.
func runFailedOf(rc runCtx) *runFailPanel {
	run := rc.Run
	failures := activity.FailedTargets(run.Root)
	failed := run.Done() && run.Status == store.StatusFailed
	canceled := run.Done() && run.Status == store.StatusCanceled
	if failures == nil && !failed && !canceled {
		return nil
	}
	if failures == nil && canceled {
		return runStopped(run)
	}
	p := &runFailPanel{Title: "What failed", Class: "bad"}
	for _, f := range failures {
		g := runFailGroup{Nodes: runFoldNodes(f.Nodes), Class: f.Class, Err: f.Err}
		if len(f.Nodes) == 1 {
			g.Err = strings.ReplaceAll(g.Err, "{}", f.Nodes[0])
		}
		p.Groups = append(p.Groups, g)
		p.Doctor = p.Doctor || f.Class == "timeout"
	}
	if failures == nil {
		p.CmdErr = run.Err
	}
	if canceled {
		p.Title, p.Class = "Stopped", "warn"
	}
	if note := runRollbackNote(run); note != "" {
		p.Notes = append(p.Notes, note)
	}
	return p
}

// runStopped builds the panel of a run that was stopped without a failure.
func runStopped(run store.Run) *runFailPanel {
	stopped, never := 0, 0
	runVisit(run.Root, func(t store.Span, _ string) {
		switch {
		case t.Kind != activity.KindTarget:
		case t.Started.IsZero():
			never++
		case t.Done() && t.Status == store.StatusOK:
		default:
			stopped++
		}
	})
	p := &runFailPanel{Title: "Stopped", Class: "warn"}
	p.Notes = append(p.Notes, fmt.Sprintf("The run was interrupted. %s stopped while they ran, and %s never started.",
		runPlural(stopped, "target"), runPlural(never, "target")))
	if note := runRollbackNote(run); note != "" {
		p.Notes = append(p.Notes, note)
	}
	return p
}

// runPlural writes a count and its noun: "1 target", "3 targets".
func runPlural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// runRollbackNote says what became of the rollback step of a run, or is
// empty for a run without one.
func runRollbackNote(run store.Run) string {
	for _, s := range activity.VisibleChildren(run.Root) {
		if s.Kind != activity.KindStep || s.Name != "rollback" {
			continue
		}
		switch runSpanClass(s, run.Done()) {
		case runOK:
			return "sind removed what the command had created (rollback). Nothing is left behind."
		case runFailed:
			note := "The rollback failed, so some of what the command created may be left behind."
			if s.Err != "" {
				note += " " + s.Err
			}
			return note
		case runQueued, runRunning:
			return "sind is removing what the command had created (rollback)."
		}
		return "The rollback did not finish, so some of what the command created may be left behind."
	}
	return ""
}

var runNameRE = regexp.MustCompile(`^(.*?)([1-9][0-9]*|0)$`)

// runFoldNodes writes a set of node names the short way, as the terminal
// does: worker-[2,5] for worker-2 and worker-5, worker-[0-3] for four in a
// row. Names without a number at the end stay as they are.
func runFoldNodes(names []string) string {
	type group struct {
		prefix string
		nums   []int
		plain  bool
	}
	var groups []*group
	index := map[string]*group{}
	for _, name := range names {
		m := runNameRE.FindStringSubmatch(name)
		key, plain := name, true
		var n int
		if m != nil {
			key, plain = m[1], false
			n, _ = strconv.Atoi(m[2])
		}
		k := key
		if plain {
			k = "\x00" + key
		}
		g := index[k]
		if g == nil {
			g = &group{prefix: key, plain: plain}
			index[k] = g
			groups = append(groups, g)
		}
		if !plain {
			g.nums = append(g.nums, n)
		}
	}
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		if g.plain {
			out = append(out, g.prefix)
			continue
		}
		sort.Ints(g.nums)
		var parts []string
		for i := 0; i < len(g.nums); {
			j := i
			for j+1 < len(g.nums) && g.nums[j+1] <= g.nums[j]+1 {
				j++
			}
			switch {
			case g.nums[j]-g.nums[i] >= 2:
				parts = append(parts, fmt.Sprintf("%d-%d", g.nums[i], g.nums[j]))
			case j > i && g.nums[j] > g.nums[i]:
				parts = append(parts, strconv.Itoa(g.nums[i]), strconv.Itoa(g.nums[j]))
			default:
				parts = append(parts, strconv.Itoa(g.nums[i]))
			}
			i = j + 1
		}
		if len(parts) == 1 && !strings.ContainsAny(parts[0], "-") {
			out = append(out, g.prefix+parts[0])
			continue
		}
		out = append(out, g.prefix+"["+strings.Join(parts, ",")+"]")
	}
	return strings.Join(out, ", ")
}
