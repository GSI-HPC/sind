// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"cmp"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/internal/ui/activity"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

// The outcome of a run that was stopped says who stopped it and where,
// what the nodes were doing at that time, and what sind undid.

// runStoppedTitle names where the run was stopped from.
func runStoppedTitle(rc runCtx) string {
	switch runStopSource(rc) {
	case runFromUI:
		return "Stopped from sind ui"
	case runFromProgram:
		return "Stopped by another program"
	}
	return "Stopped from the terminal"
}

// runStopSource is where the run came from: sind ui for the run of a
// task.
func runStopSource(rc runCtx) string {
	if rc.Task != nil {
		return runFromUI
	}
	return runSource(rc.Snap, rc.Run)
}

// runStoppedLead says who stopped the run, and after how long; while it
// waited for the lock of its realm, that nothing changed.
func runStoppedLead(rc runCtx) string {
	who := "Someone stopped the command in its terminal (Ctrl-C)"
	switch runStopSource(rc) {
	case runFromUI:
		who = "Someone stopped the run from another sind ui"
		if rc.Task != nil {
			who = "You stopped the run from sind ui"
		}
	case runFromProgram:
		who = "The program that started the run stopped it"
	}
	if runStoppedInLockWait(rc.Run) {
		return who + " while it waited for the realm lock. Nothing changed."
	}
	return who + " after " + runDur(runElapsed(rc.Run, rc.Now)) + "."
}

// runStoppedInLockWait reports whether a run was stopped while it waited
// for the lock of its realm, before any step began.
func runStoppedInLockWait(run store.Run) bool {
	waited := false
	for _, s := range activity.VisibleChildren(run.Root) {
		switch {
		case s.Kind == activity.KindWait && s.Name == runLockWait:
			waited = s.Status != store.StatusOK
		case s.Kind == activity.KindStep && !s.Started.IsZero():
			return false
		}
	}
	return waited
}

// runPhase is what a target of a stopped run was doing, in its phase
// (runCurPhase): Noun names it for the Result column of Activity
// (container, services or setup), and One and Many say it for one node and
// for several: "was starting its container", "were starting their
// containers".
type runPhase struct{ Noun, One, Many string }

// runCurPhase returns the phase of a target that had not ended: the first
// call or wait that started and did not end ok. False for none, as for a
// target that was running between them.
func runCurPhase(t store.Span) (store.Span, bool) {
	for _, p := range t.Children {
		if !p.Started.IsZero() && p.Status != store.StatusOK {
			return p, true
		}
	}
	return store.Span{}, false
}

// runPhaseOf classifies a phase: a wait for the container or for systemd,
// a pull, a docker create or start is the container; a wait for a
// service, such as munge, is services; anything else is setup.
func runPhaseOf(p store.Span) runPhase {
	switch {
	case p.Kind == activity.KindWait && (p.Message == "" || p.Message == "container"):
		return runPhase{"container", "waiting for its container", "waiting for their containers"}
	case p.Kind == activity.KindWait && p.Message == "systemd":
		return runPhase{"container", "waiting for systemd", "waiting for systemd"}
	case p.Kind == activity.KindWait:
		return runPhase{"services", "waiting for " + p.Message, "waiting for " + p.Message}
	case strings.HasPrefix(p.Name, "docker pull"):
		return runPhase{"container", "pulling its image", "pulling their images"}
	case p.Name == "docker create" || p.Name == "docker start":
		return runPhase{"container", "starting its container", "starting their containers"}
	}
	w := "in " + strings.TrimPrefix(p.Name, "docker ")
	return runPhase{"setup", w, w}
}

// runStoppedNodes groups the nodes of a stopped run that had not ended by
// their phase, in the order of the log, and counts the nodes that never
// started. A node between two phases is running.
func runStoppedNodes(run store.Run) (order []runPhase, nodes map[runPhase][]string, never int) {
	nodes = map[runPhase][]string{}
	runVisit(run.Root, func(t store.Span, step string) {
		if t.Kind != activity.KindTarget || step == "rollback" {
			return
		}
		switch {
		case t.Started.IsZero():
			never++
			return
		case t.Done() && t.Status != store.StatusCanceled:
			return
		}
		ph := runPhase{"setup", "running", "running"}
		if p, ok := runCurPhase(t); ok {
			ph = runPhaseOf(p)
		}
		if _, ok := nodes[ph]; !ok {
			order = append(order, ph)
		}
		nodes[ph] = append(nodes[ph], cmp.Or(t.Node, t.Name))
	})
	return order, nodes, never
}

// runStoppedDoing says what the nodes did when the run was stopped, one
// sentence for the nodes in each phase, and how many never started.
func runStoppedDoing(run store.Run) []string {
	order, nodes, never := runStoppedNodes(run)
	var out []string
	for _, ph := range order {
		line := " was " + ph.One
		if len(nodes[ph]) > 1 {
			line = " were " + ph.Many
		}
		out = append(out, runFoldNodes(nodes[ph])+line+" at that time.")
	}
	if never > 0 {
		out = append(out, pgPlural(never, "node", "nodes")+" had not started yet.")
	}
	return out
}

// runStoppedUndo says what the rollback of a stopped run undid, and for a
// create worker that its cluster is as it was before the run.
func runStoppedUndo(rc runCtx) []string {
	run := rc.Run
	i := slices.IndexFunc(activity.VisibleChildren(run.Root), func(s store.Span) bool {
		return s.Kind == activity.KindStep && s.Name == "rollback"
	})
	if i < 0 {
		return nil
	}
	rb := activity.VisibleChildren(run.Root)[i]
	if runSpanClass(rb, run.Done()) != runOK {
		return runRollbackNotes(run)
	}
	took := ""
	if rb.Ended != nil {
		took = ", " + runDur(rb.Ended.Sub(rb.Started))
	}
	var made []string
	runVisit(run.Root, func(t store.Span, step string) {
		if t.Kind == activity.KindTarget && t.Role != "" && step != "rollback" && !t.Started.IsZero() && !slices.Contains(made, t.Node) {
			made = append(made, t.Node)
		}
	})
	out := []string{"sind removed what the command had made (rollback" + took + ")."}
	if len(made) > 0 {
		out[0] = "sind removed " + runFoldNodes(made) + " again (rollback" + took + ")."
	}
	e := runEntryOf(rc.Snap, run)
	if run.Root.Name == "create worker" && e.realm != "" {
		if c, ok := rc.Snap.Cluster(e.realm, e.cluster); ok {
			out = append(out, e.cluster+" is as it was before the run: "+pgPlural(c.NodeCount, "node", "nodes")+".")
		}
	}
	return out
}
