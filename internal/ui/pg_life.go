// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/activity"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

// What the runs that go on do to a cluster and its nodes: a run that
// creates or deletes the cluster owns it as a whole, and a run that adds or
// deletes workers owns the nodes it names as its targets. The pages show
// what such a run owns as being created or deleted, with its progress,
// instead of the faults of a node that is not up yet, or no longer.

// The kinds of work on a cluster or a node.
const (
	pgCreating = "creating"
	pgDeleting = "deleting"
)

// pgLifeRuns are the commands that create or delete what they work on, by
// the first words of their title, and the work they do on it; the cluster
// ones own the whole cluster, the worker ones the nodes of their targets.
var pgLifeRuns = []pgLifeRun{
	{"create cluster ", pgCreating, true},
	{"delete cluster ", pgDeleting, true},
	{"create worker ", pgCreating, false},
	{"delete worker ", pgDeleting, false},
}

// pgLifeRun is a command that creates or deletes what it works on.
type pgLifeRun struct {
	Prefix, Kind string
	Cluster      bool
}

// pgPhase is where a node stands in the run that creates or deletes it:
// the class of its target (ok, running, queued or failed) and a short
// line, such as "ready in 22s" or "waits for munge · 19s of 5m".
type pgPhase struct {
	Node, Role string
	State      string
	Text       string
}

// Mark returns the sign of the phase, for a node card.
func (p pgPhase) Mark() string {
	switch p.State {
	case runOK:
		return "✓"
	case runFailed:
		return "✗"
	case runQueued, runRunning:
		return "○"
	}
	return "–"
}

// pgLife is a run that creates or deletes a cluster as a whole, as its
// pages show it.
type pgLife struct {
	// Kind is pgCreating or pgDeleting, Name the cluster.
	Kind, Name string
	Run        pgRun
	// Step is what the run does now, as "nodes 1/3"; empty between steps.
	Step string
	// Segs is the bar of the run's steps and Steps their names, in order.
	Segs  runBar
	Steps []pgPill
	// Started is when the run started, as a time of day; Elapsed and
	// Since its clock.
	Started, Elapsed string
	Since            int64
	// Nodes are where the nodes stand that the run names as targets.
	Nodes []pgPhase
	// Stop is the Stop button of a run of sind ui; its CSRF is set by the
	// page (pgStopCSRF).
	Stop *runStop
}

// Title is the title of the run, which its bar is named after.
func (l *pgLife) Title() string { return l.Run.Title }

// Source says where the run came from, as "from sind ui".
func (l *pgLife) Source() string { return runSourceText(l.Run.Source) }

// Text says what the run does, for a state pill: "creating · nodes 1/3"
// with the step it is in, "deleting · 8 of 11 left" with the nodes that
// are left, so that it does not read as the progress of a create, or,
// while it waits for the lock of its realm, "create queued".
func (l *pgLife) Text() string {
	switch {
	case l.Run.Waiting:
		return strings.TrimSuffix(l.Kind, "ing") + "e queued"
	case l.Kind == pgDeleting && len(l.Nodes) > 0:
		left := pgCount(l.Nodes, func(p pgPhase) bool { return p.State != runOK })
		return fmt.Sprintf("%s · %d of %d left", l.Kind, left, len(l.Nodes))
	case l.Kind == pgCreating && l.Step != "":
		return l.Kind + " · " + l.Step
	}
	return l.Kind
}

// Pct is the part of the run that is done, in whole percent.
func (l *pgLife) Pct() int { return l.Segs.Pct() }

// Verb is what the run does to the cluster, for a sentence: "creates",
// "deletes".
func (l *pgLife) Verb() string { return strings.TrimSuffix(l.Kind, "ing") + "es" }

// pgWork is what the runs that go on do to one cluster.
type pgWork struct {
	// Busy is set while a run works on the cluster (pgWorksOn).
	Busy bool
	// Life is the run that creates or deletes the whole cluster, nil for
	// none.
	Life *pgLife
	// nodes are the nodes that a run creates or deletes, by short name,
	// with the kind of its work.
	nodes map[string]pgNodeWork
}

// pgNodeWork is what a run does to one node.
type pgNodeWork struct {
	Kind  string
	Phase pgPhase
	Run   pgRun
}

// pgWorkOf returns what the runs that go on do to the cluster: whether one
// works on it, the one that creates or deletes it, and the nodes that runs
// that add or delete workers name as their targets. Of several runs that
// create or delete the cluster, the one that does not wait for the lock of
// its realm wins, else the oldest.
func pgWorkOf(sn store.Snapshot, realm, name string, now time.Time) pgWork {
	var w pgWork
	var life *store.Run
	for _, r := range slices.Backward(sn.Runs) {
		if !pgWorksOn(sn, r, realm, name) {
			continue
		}
		w.Busy = true
		i := slices.IndexFunc(pgLifeRuns, func(l pgLifeRun) bool { return strings.HasPrefix(r.Title, l.Prefix) })
		switch {
		case i < 0:
		case pgLifeRuns[i].Cluster:
			if life == nil || runWaitsForLock(*life) && !runWaitsForLock(r) {
				life = &r
				w.Life = pgLifeOf(sn, r, pgLifeRuns[i].Kind, name, now)
			}
		default:
			for _, p := range pgPhases(r, pgLifeRuns[i].Kind, now) {
				if w.nodes == nil {
					w.nodes = map[string]pgNodeWork{}
				}
				w.nodes[p.Node] = pgNodeWork{Kind: pgLifeRuns[i].Kind, Phase: p, Run: pgRunOf(sn, r, now)}
			}
		}
	}
	if w.Life != nil {
		w.nodes = map[string]pgNodeWork{}
		for _, p := range w.Life.Nodes {
			w.nodes[p.Node] = pgNodeWork{Kind: w.Life.Kind, Phase: p, Run: w.Life.Run}
		}
	}
	return w
}

// apply marks the node with the work on it: Busy, and Creating or Deleting
// with its phase, for every node of a cluster that a run creates or
// deletes, and for a node that a run names as its target.
func (w pgWork) apply(n *pgNode) {
	n.Busy = w.Busy
	kind := ""
	if w.Life != nil {
		kind = w.Life.Kind
	}
	if nw, ok := w.nodes[n.Short]; ok {
		kind = nw.Kind
		p := nw.Phase
		n.Phase = &p
	}
	n.Creating, n.Deleting = kind == pgCreating, kind == pgDeleting
}

// runOf returns the run that creates or deletes the node, ok false for
// none.
func (w pgWork) runOf(short string) (pgRun, bool) {
	if nw, ok := w.nodes[short]; ok {
		return nw.Run, true
	}
	if w.Life != nil {
		return w.Life.Run, true
	}
	return pgRun{}, false
}

// pgLifeOf builds the life of a cluster from the run that creates or
// deletes it.
func pgLifeOf(sn store.Snapshot, r store.Run, kind, name string, now time.Time) *pgLife {
	shown := runRevealed(r, now)
	l := &pgLife{
		Kind: kind, Name: name, Run: pgRunOf(sn, r, now), Segs: runSegs(shown),
		Started: runWhen(r.Start, now), Elapsed: runDur(runElapsed(r, now)), Since: runSince(r),
		Nodes: pgPhases(shown, kind, now),
	}
	for _, s := range activity.VisibleChildren(shown.Root) {
		if s.Kind != activity.KindStep {
			continue
		}
		l.Steps = append(l.Steps, pgPill{s.Name, runSpanClass(s, r.Done())})
		if l.Step == "" && !s.Done() && s.State == activity.StateRunning {
			l.Step = s.Name
			if done, total := runStepCounts(s); total > 0 {
				l.Step += fmt.Sprintf(" %d/%d", done, total)
			}
		}
	}
	if t, ok := runTaskOf(sn, r.Trace); ok {
		l.Stop = newRunStop(t, &r, l.Run.Title, "", false)
	}
	if l.Stop != nil {
		l.Stop.Label = "Stop the " + strings.TrimSuffix(kind, "ing") + "e…"
	}
	return l
}

// pgPhases returns where each node stands that a run names as a target,
// in the order of their first target: the last target of the node that
// has begun, as a node of create cluster is a target of the step nodes and
// later of the step slurm.
func pgPhases(r store.Run, kind string, now time.Time) []pgPhase {
	var out []pgPhase
	runVisit(r.Root, func(s store.Span, step string) {
		if s.Kind != activity.KindTarget || s.Node == "" || step == "" || step == "pull images" {
			return
		}
		p := pgPhase{Node: s.Node, Role: s.Role, State: runSpanClass(s, r.Done())}
		p.Text = pgPhaseText(s, p.State, step, kind, now)
		i := slices.IndexFunc(out, func(o pgPhase) bool { return o.Node == s.Node })
		switch {
		case i < 0:
			out = append(out, p)
		case p.State != runQueued:
			out[i] = p
		}
	})
	return out
}

// pgPhaseText says where the target s of a node stands, in the step: how
// long it took once it is done, what it waits for while it runs, as
// "waits for munge · 19s of 5m", else what it does, or why it failed.
func pgPhaseText(s store.Span, class, step, kind string, now time.Time) string {
	switch class {
	case runQueued:
		return "queued"
	case runFailed:
		line, _, _ := strings.Cut(strings.TrimSpace(strings.ReplaceAll(s.Err, "{}", s.Node)), "\n")
		return line
	case runRunning:
	case runOK:
		done := "removed"
		switch {
		case kind == pgDeleting:
		case step == "nodes":
			done = "ready"
		default:
			done = step + " done"
		}
		if s.Ended != nil && !s.Started.IsZero() {
			done += " in " + runDur(s.Ended.Sub(s.Started))
		}
		return done
	default:
		return "stopped"
	}
	for _, c := range activity.VisibleChildren(s) {
		if c.Kind != activity.KindWait || c.Done() || c.State != activity.StateRunning {
			continue
		}
		text := "waits for " + pgOr(c.Message, c.Name) + " · " + runDur(now.Sub(c.Started))
		if c.Timeout > 0 {
			text += " of " + pgBound(c.Timeout)
		}
		return text
	}
	switch {
	case kind == pgDeleting:
		return "removing"
	case step == "nodes":
		return "starting the container"
	}
	return "starting " + step
}

// pgBound writes the bound of a wait as runDur does, without the zero
// seconds or minutes of a round one: "5m", "1h", "1m30s".
func pgBound(d time.Duration) string {
	out := runDur(d)
	for _, zero := range []string{"m00s", "h00m"} {
		if strings.HasSuffix(out, zero) {
			return strings.TrimSuffix(out, zero[1:])
		}
	}
	return out
}

// pgOr returns a, or b when a is empty.
func pgOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
