// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"strconv"
	"strings"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

// The outcome of a run sits right under the head of its page: what came of
// the command, in the words of the cluster, and what to do next. A create
// cluster that ended ok says that the cluster is ready for jobs, with the
// checks of the cluster's Summary and the first commands of a job.

// runOutcome is the panel of the outcome of a run.
type runOutcome struct {
	// Class colours the panel: ok, bad or warn; Mark is the sign in its
	// circle.
	Class, Mark string
	Title       string
	// Meta sums the run up beside the title: "created in 50s · 3 nodes",
	// and Realm, when set, follows it as a chip.
	Meta  string
	Realm string
	// Ready is the "Ready for jobs" card of the cluster that a create
	// made, whose checks and commands the panel shows.
	Ready *pgReady
	// Fail is what failed, for a run that did not end ok. WhyTitle and
	// Why say what often causes it, and Doctor are the host checks that
	// did not pass, which DoctorText counts.
	Fail       *runFailPanel
	WhyTitle   string
	Why        []string
	Doctor     []store.DoctorCheck
	DoctorText string
	// Next are the ways on, and NextNote a word about them. CopyErr, when
	// set, is the error that the button Copy the error copies.
	Next     []pgAction
	NextNote string
	CopyErr  string
}

// runOutcomeOf builds the outcome of the run: what failed, once a target
// or the run failed or the run was stopped, and what came of a create
// cluster that ended ok; nil for a run that goes on well, or one whose
// outcome says no more than its head does.
func runOutcomeOf(rc runCtx) *runOutcome {
	if p := runFailedOf(rc); p != nil {
		return runFailOutcome(rc, p)
	}
	run := rc.Run
	if !run.Done() || run.Status != store.StatusOK {
		return nil
	}
	return runCreated(rc)
}

// runCreated is the outcome of a create cluster that ended ok, while the
// cluster exists: it is ready for jobs, as the checks of its Summary say,
// and the first commands of a job follow. Nil for another run, or a
// cluster that sind ui does not know in one realm.
func runCreated(rc runCtx) *runOutcome {
	e := runEntryOf(rc.Snap, rc.Run)
	if rc.Run.Root.Name != "create cluster" || e.realm == "" || e.cluster == "" {
		return nil
	}
	d, _ := pgClusterViewOf(rc.Snap, e.realm, e.cluster, "", rc.Now, rc.CSRF)
	if !d.Found {
		return nil
	}
	o := &runOutcome{
		Class: "ok", Mark: "✓", Title: e.cluster + " is ready for jobs", Ready: d.Ready,
		Meta: strings.Join([]string{"created in " + runDur(runElapsed(rc.Run, rc.Now)), d.Cluster.NodesText()}, " · "), Realm: e.realm,
		Next: []pgAction{
			{Label: "All connect commands", URL: pgClusterURL(e.realm, e.cluster, "connect")},
			pgDialogAction("Add workers…", "", "create-worker", "realm", e.realm, "cluster", e.cluster),
		},
		NextNote: "The cluster Summary shows the same checks.",
	}
	for i := range d.Ready.Commands {
		// The first commands of a job, in the order to run them.
		d.Ready.Commands[i].Label = strconv.Itoa(i+1) + " · " + d.Ready.Commands[i].Label
	}
	switch d.Ready.Class {
	case "bad":
		o.Class, o.Mark, o.Title = "warn", "!", e.cluster+" was created, but it cannot run jobs"
	case "warn":
		o.Title = e.cluster + " is ready for jobs, " + strings.TrimPrefix(d.Ready.Verdict, "yes, ")
	case "ok":
	default:
		// Not probed yet, or a command works on it again.
		o.Title = e.cluster + " was created"
	}
	return o
}
