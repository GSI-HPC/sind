// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"cmp"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/internal/ui/activity"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/mesh"
)

func init() { registerRun(defaultRegistry) }

// runFragments are the live parts of the run page, in the order the page
// places them. Each depends on the one run that the page shows. The
// summary and the timeline are no fragments: gantt.js draws them from the
// data of run-summary (runGantt).
var runFragments = []string{"run-head", "run-outcome", "run-command", "run-summary", "run-facts"}

// runOutcomePrefixes are the topics that the outcome of a run depends on
// beside the run and the Doctor report: the clusters, their nodes and their
// status, which its checks of a cluster that a create made read.
var runOutcomePrefixes = []string{"clusters/", "nodes/", "status/"}

// registerRun adds the progress display to r: the run page, the Activity
// page, the page of a task whose run the log does not hold yet, and the
// routes of the task dock. The dock itself is the shell fragment "tasks".
func registerRun(r *Registry) {
	loads := map[string]func(rc runCtx) any{
		"run-head":    func(rc runCtx) any { return runHeadOf(rc) },
		"run-outcome": func(rc runCtx) any { return runOutcomeOf(rc) },
		"run-command": func(rc runCtx) any { return runHeadOf(rc) },
		"run-summary": func(rc runCtx) any { return runSummaryOf(rc) },
		"run-facts":   func(rc runCtx) any { return runFactsOf(rc) },
	}
	for _, name := range runFragments {
		build := loads[name]
		var prefixes []string
		var topics []store.Topic
		if name == "run-outcome" {
			prefixes, topics = runOutcomePrefixes, []store.Topic{store.TopicDoctor}
		}
		r.AddFragment(Fragment{
			Name:     name,
			Topics:   topics,
			Params:   []string{"id"},
			Prefixes: prefixes,
			Watch:    func(p Params) []store.Topic { return []store.Topic{store.RunTopic(p.Get("id"))} },
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
	// CSRF is the session's token, for the forms.
	CSRF string
}

// newRunCtx finds the run named by the Param id of v.
func newRunCtx(v *View) runCtx {
	rc := runCtx{Snap: v.Snap, Now: v.Now, CSRF: v.CSRF}
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
	rc := newRunCtx(v)
	if !rc.Found {
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
	v.Title = cmp.Or(rc.Run.Title, "Run "+id)
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
	Title     string
	Pill      string
	PillClass string
	// NoStop says why sind ui cannot stop a run that goes on and that it
	// did not start.
	NoStop   string
	Origin   string
	Program  string
	Started  string
	Exit     string
	Trace    string
	TraceTip string
	// Back leads to what the run's task worked on: its cluster, the list
	// of clusters once the cluster is gone, or the mesh.
	Back *runLink
	// Command is the command line of the run's task, with Copy, or for a
	// run without a task the command that its title names; CmdNote then
	// says that the log keeps no flags.
	Command *pgCmd
	CmdNote string
	// CopyNote, for a run without a task, is what Copy with a note copies:
	// a comment that the log keeps no flags, and the command.
	CopyNote string
	// Fold folds the command and the config into one line, once the run
	// ended ok: its outcome comes first then.
	Fold bool
	// Realm is the realm the run worked on, when sind ui knows it.
	Realm string
	// Config is the file that the task saved and ran with.
	Config *pgCmd
	// RetryID is the task that Try again runs again, when it runs it
	// without a dialog; Edit opens the dialog filled in with its form.
	RetryID string
	Edit    *runLink
	// RetryPrimary makes Try again the button to press, unless Edit is.
	RetryPrimary bool
	CSRF         string
	// TaskID is the task to stop; empty when sind ui cannot stop the run.
	// Stop is its button, nil while it is being stopped.
	TaskID    string
	Stop      *runStop
	Canceling bool
}

// runHeadOf builds the head of the run page.
func runHeadOf(rc runCtx) *runHead {
	run := rc.Run
	h := &runHead{
		Title: run.Title, Origin: runSourceText(runSource(rc.Snap, run)),
		Started: runWhen(run.Start, rc.Now), Trace: run.Trace, TraceTip: run.Trace,
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
	if rc.Task == nil && !run.Done() {
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
		h.CSRF = rc.CSRF
		if !run.Done() {
			h.TaskID, h.Canceling = t.ID, t.Canceling
			h.Stop = newRunStop(*t, &rc.Run, run.Title, rc.CSRF, false)
		}
		h.Back = runBack(rc.Snap, *t)
		if t.Display != "" {
			h.Command = &pgCmd{Label: "Command", Text: t.Display}
		}
		if t.ConfigPath != "" {
			h.Config = &pgCmd{Label: t.ConfigPath, Text: t.Config}
		}
		if run.Done() {
			h.RetryID, h.Edit = runRetry(*t, runStatusClass(run))
			if h.Edit != nil && t.Action == "create-cluster" && runTimedOut(run) {
				// Nodes ran out of the wait: a longer one is what to try.
				kv, word := runRetryWait(run)
				h.Edit = &runLink{Label: "Try again with a " + word + " wait…", Href: h.Edit.Href + "&" + strings.TrimPrefix(pgQueryURL("", kv...), "?"), Primary: true}
			}
		}
		h.Realm = t.Realm
	} else {
		runHeadNoTask(rc, h)
	}
	h.RetryPrimary = h.RetryID != "" && (h.Edit == nil || !h.Edit.Primary)
	if run.Done() && runStatusClass(run) == runOK {
		// What the command worked on is the way on.
		h.Fold = h.Command != nil
		if h.Back != nil {
			h.Back.Primary = true
		}
	}
	return h
}

// runHeadNoTask fills in the head of a run without a task, as one from a
// terminal or one whose task sind ui no longer keeps: the command that its
// title names, with the realm when only one realm has its cluster; once it
// failed or was stopped, the dialog that does it again where there is one,
// filled in from the title; and the way to its cluster.
func runHeadNoTask(rc runCtx, h *runHead) {
	run := rc.Run
	e := runEntryOf(rc.Snap, run)
	h.Realm = e.realm
	if run.Title == "" {
		return
	}
	h.Command = &pgCmd{Label: "What the event log names", Text: action.Quote(sindArgvOf(e.realm, runLogWords(run)...))}
	h.CmdNote = "Its flags and config are not in the log."
	if runSource(rc.Snap, run) == runFromUI {
		h.CmdNote = "sind ui no longer keeps its form, and the log has no flags or config."
	}
	add := "add the ones it had."
	if run.Root.Name == "create cluster" {
		add = "add them, such as --config FILE."
	}
	h.CopyNote = "# The event log keeps no flags of this command; " + add + "\n" + h.Command.Text
	if e.cluster != "" {
		h.Back = &runLink{Label: "Open Clusters", Href: "/clusters"}
		if e.realm != "" {
			h.Back = &runLink{Label: "Open " + e.cluster, Href: pgClusterURL(e.realm, e.cluster, "")}
		}
	}
	class := runStatusClass(run)
	if !run.Done() || (class != runFailed && class != runCanceled) {
		return
	}
	words := strings.Fields(run.Title)
	switch strings.Join(words[:min(2, len(words))], " ") {
	case "create cluster":
		h.Edit = &runLink{Label: "Edit and try again…", Href: pgDialog("create-cluster", "realm", e.realm, "name", e.cluster)}
		if runTimedOut(run) {
			// Nodes ran out of the wait: a longer one is what to try.
			kv, word := runRetryWait(run)
			h.Edit = &runLink{Label: "Try again with a " + word + " wait…", Href: pgDialog("create-cluster", append([]string{"realm", e.realm, "name", e.cluster}, kv...)...), Primary: true}
		}
	case "create worker":
		if e.realm != "" {
			h.Edit = &runLink{Label: "Edit and try again…", Href: pgDialog("create-worker", "realm", e.realm, "cluster", e.cluster)}
		}
	}
}

// runLogWords are the words of the command of a run without a task, as
// sind takes them, from what the log names: the nodes of a power command
// or a delete worker, as its title names them, and for a create worker
// the cluster with --count for the workers it made.
func runLogWords(run store.Run) []string {
	words := strings.Fields(cmp.Or(run.Command, run.Title))
	name := run.Root.Name
	switch {
	case name == "create worker":
		if n := len(activity.NodeNames(run.Root)); n > 1 {
			words = append(words, "--count", strconv.Itoa(n))
		}
	case (strings.HasPrefix(name, "power ") || name == "delete worker") && run.Command != "":
		words = strings.Fields(run.Title)
	}
	return words
}

// sindArgvOf is the command line of sind in realm, which names a realm
// other than the default one; an empty realm is the default one.
func sindArgvOf(realm string, words ...string) []string {
	argv := []string{"sind"}
	if realm != "" && realm != mesh.DefaultRealm {
		argv = append(argv, "--realm", realm)
	}
	return append(argv, words...)
}

// runTimedOut reports whether a run failed as a node ran out of time.
func runTimedOut(run store.Run) bool {
	if run.Class == "timeout" {
		return true
	}
	for _, f := range activity.FailedTargets(run.Root) {
		if f.Class == "timeout" {
			return true
		}
	}
	return false
}

// runLink is a link of the run page; Primary makes it the button to
// press.
type runLink struct {
	Label, Href string
	Primary     bool
}

// runBack leads from the run of a task to what it worked on: its cluster
// while it exists, the list of clusters once it is gone, and the mesh for
// a command of the realm. Nil for a task of none of them.
func runBack(snap store.Snapshot, t store.Task) *runLink {
	_, exists := snap.Cluster(t.Realm, t.Cluster)
	switch {
	case t.Cluster != "" && exists:
		return &runLink{Label: "Open " + t.Cluster, Href: "/clusters/" + t.Realm + "/" + t.Cluster}
	case t.Cluster != "":
		return &runLink{Label: "Open Clusters", Href: "/clusters"}
	case t.Action == "connect" || t.Action == "disconnect" || t.Action == "delete-realm":
		return &runLink{Label: "Open Mesh and DNS", Href: "/mesh"}
	}
	return nil
}

// runRetry offers to try the command of a task that failed or was stopped
// again. A command that wants no typed confirmation gets Try again, which
// runs it at once, and Edit and try again, which opens its dialog filled
// in; one that wants the user to type a name gets Try again… alone, which
// opens the dialog. Nothing for a command whose class is not failed or
// canceled, or for a task that no form started.
func runRetry(t store.Task, class string) (string, *runLink) {
	if (class != runFailed && class != runCanceled) || t.Form == nil {
		return "", nil
	}
	a, ok := action.Lookup(t.Action)
	if !ok {
		return "", nil
	}
	if a.Want(url.Values(t.Form)) != "" {
		return "", &runLink{Label: "Try again…", Href: actEditHref(t)}
	}
	return t.ID, &runLink{Label: "Edit and try again…", Href: actEditHref(t)}
}

// runSummary is what the run page draws its summary and its timeline from,
// and what it puts in the browser tab and announces.
type runSummary struct {
	// Data is the run as gantt.js reads it (runGantt).
	Data string
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

// runSummaryOf builds the summary. What the page says of hidden spans,
// such as the tab's title and what failed, follows the terminal, which
// shows a hidden step that takes long (activity.Reveal); gantt.js does the
// same as it draws.
func runSummaryOf(rc runCtx) *runSummary {
	s := &runSummary{Data: runGanttOf(rc).JSON()}
	run := runRevealed(rc.Run, rc.Now)
	canceling := false
	s.SayKey = "r:" + run.ID
	if t := rc.Task; t != nil {
		canceling, s.SayKey = t.Canceling, "t:"+t.ID
	}
	s.SayState = runSayState(runStatusClass(run), canceling)
	s.Say = runSay(run.Title, s.SayState, runDur(runElapsed(run, rc.Now)), runFailure(run))
	if origin := runOrigin(run); !origin.IsZero() {
		s.SaySince = origin.UnixMilli()
	}
	s.DocTitle = runDocTitle(run)
	return s
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

// runFailPanel says what failed in a run, or what was going on when it
// was stopped, for its outcome.
type runFailPanel struct {
	// Class is the callout class of the groups: bad or warn.
	Class  string
	Groups []runFailGroup
	// CmdErr is the error of the command, when no target gave one.
	CmdErr string
	// Says say in plain words what the nodes that ran out of time waited
	// for (runTimeoutSays).
	Says  []runPlain
	Notes []string
}

// runFailedOf builds the panel; nil for a run that has not failed or been
// stopped.
func runFailedOf(rc runCtx) *runFailPanel {
	run := runRevealed(rc.Run, rc.Now)
	failures := activity.FailedTargets(run.Root)
	failed := run.Done() && run.Status == store.StatusFailed
	canceled := run.Done() && run.Status == store.StatusCanceled
	if failures == nil && !failed && !canceled {
		return nil
	}
	if failures == nil && canceled {
		return runStopped(run)
	}
	p := &runFailPanel{Class: "bad"}
	for _, f := range failures {
		g := runFailGroup{Nodes: runFoldNodes(f.Nodes), Class: f.Class, Err: f.Err}
		if len(f.Nodes) == 1 {
			g.Err = strings.ReplaceAll(g.Err, "{}", f.Nodes[0])
		}
		p.Groups = append(p.Groups, g)
	}
	if failures == nil {
		p.CmdErr = run.Err
	}
	if canceled {
		p.Class = "warn"
	}
	p.Notes = runRollbackNotes(run)
	return p
}

// runRollbackNotes says what became of the rollback of a run where it may
// have left something behind; the line beside the outcome says when it
// went well (runRollbackWord).
func runRollbackNotes(run store.Run) []string {
	if note := runRollbackNote(run); note != "" && !strings.HasSuffix(note, "Nothing is left behind.") {
		return []string{note}
	}
	return nil
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
	p := &runFailPanel{Class: "warn"}
	p.Notes = append(p.Notes, fmt.Sprintf("The run was interrupted. %s stopped while they ran, and %s never started.",
		pgPlural(stopped, "target", "targets"), pgPlural(never, "target", "targets")))
	p.Notes = append(p.Notes, runRollbackNotes(run)...)
	return p
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
