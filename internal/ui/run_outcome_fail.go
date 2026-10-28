// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/GSI-HPC/go-clikit/doctor"
	"github.com/GSI-HPC/sind/internal/ui/activity"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

// The outcome of a run that failed says first what did not happen, in the
// words of the cluster ("bench was not created"), then what failed and why
// that often happens, with the host checks of Doctor that did not pass.

// runPlain is a plain sentence about what failed. Word, when set, is a word
// of help that the sentence starts with, as munge; Text follows it.
type runPlain struct {
	Word string
	Text string
}

// runTimeout is a node that ran out of its wait: the service that it
// waited for last, and how long the wait was; zero when the log does not
// say.
type runTimeout struct {
	Node    string
	Service string
	Wait    time.Duration
}

// runTimeouts returns the targets of the run that ran out of time, in the
// order of the log, with what they waited for.
func runTimeouts(run store.Run) []runTimeout {
	var out []runTimeout
	activity.Walk(run.Root, func(t store.Span) {
		if t.Kind != activity.KindTarget || t.Status != store.StatusFailed || t.Class != "timeout" {
			return
		}
		to := runTimeout{Node: cmp.Or(t.Node, t.Name)}
		activity.Walk(t, func(w store.Span) {
			if w.Kind == activity.KindWait && w.Status == store.StatusFailed {
				to.Service, to.Wait = w.Message, w.Timeout
			}
		})
		out = append(out, to)
	})
	return out
}

// runServiceWhy says what a node misses without the service; %s is the
// node or the nodes.
var runServiceWhy = map[string]string{
	"munge":      "Without munge, %s cannot talk to slurmctld.",
	"sshd":       "Without sshd, %s takes no ssh logins.",
	"slurmctld":  "Without slurmctld, the cluster has no controller.",
	"slurmd":     "Without slurmd, %s takes no jobs.",
	"mariadb":    "Without MariaDB, slurmdbd keeps no accounting.",
	"slurmdbd":   "Without slurmdbd, the cluster keeps no accounting.",
	"slurmrestd": "Without slurmrestd, the REST API does not answer.",
	"sackd":      "Without sackd, %s cannot reach slurmctld.",
}

// runTimeoutSays says in plain words what each service that nodes waited
// for did not do in time, one sentence for the nodes of each service.
func runTimeoutSays(tos []runTimeout) []runPlain {
	var order []string
	nodes := map[string][]string{}
	for _, to := range tos {
		if _, ok := nodes[to.Service]; !ok {
			order = append(order, to.Service)
		}
		nodes[to.Service] = append(nodes[to.Service], to.Node)
	}
	var out []runPlain
	for _, svc := range order {
		names := runFoldNodes(nodes[svc])
		who := "the node"
		if len(nodes[svc]) > 1 {
			who = "the nodes"
		}
		switch svc {
		case "container":
			out = append(out, runPlain{Text: "The container of " + names + " did not start in time."})
		case "systemd":
			out = append(out, runPlain{Text: "systemd on " + names + " did not finish booting in time."})
		case "":
			out = append(out, runPlain{Text: names + " did not get ready in time."})
		default:
			s := runPlain{Text: " on " + names + " did not start in time."}
			if slices.Contains(pgServiceOrder, svc) {
				s.Word = svc
			} else {
				s.Text = svc + s.Text
			}
			if why, ok := runServiceWhy[svc]; ok {
				s.Text += " " + strings.ReplaceAll(why, "%s", who)
			}
			out = append(out, s)
		}
	}
	return out
}

// runWaitWord writes a wait the way the New cluster dialog names it: 5m,
// 1h, 90s.
func runWaitWord(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	return d.String()
}

// runLongestWait returns the longest wait that a node of the run ran out
// of; zero for none the log names.
func runLongestWait(tos []runTimeout) time.Duration {
	var d time.Duration
	for _, to := range tos {
		d = max(d, to.Wait)
	}
	return d
}

// runRetryWait returns the wait to try a create cluster again with, after
// nodes ran out of the wait: the next choice of the New cluster dialog
// above it, or twice the wait above them all, as the query of the dialog
// takes it, and its word; 10m where the log names no wait.
func runRetryWait(run store.Run) (kv []string, word string) {
	d := runLongestWait(runTimeouts(run))
	if d == 0 {
		return []string{"wait", "10m"}, "10m"
	}
	for _, w := range []time.Duration{2 * time.Minute, 5 * time.Minute, 10 * time.Minute} {
		if w > d {
			return []string{"wait", runWaitWord(w)}, runWaitWord(w)
		}
	}
	word = runWaitWord(2 * d)
	return []string{"wait", "other", "wait_other", word}, word
}

// runRollbackWord says in a few words what became of the rollback of a
// run, for the line beside its outcome; empty for a run without one.
func runRollbackWord(run store.Run) string {
	for _, s := range activity.VisibleChildren(run.Root) {
		if s.Kind != activity.KindStep || s.Name != "rollback" {
			continue
		}
		switch runSpanClass(s, run.Done()) {
		case runOK:
			return "sind rolled back · nothing is left"
		case runFailed:
			return "the rollback failed"
		case runQueued, runRunning:
			return "sind rolls back"
		}
		return "the rollback did not finish"
	}
	return ""
}

// runDoctorChecks returns the host checks of the last Doctor report that
// did not pass, and a line that counts them: "Doctor: 1 failed, 1
// warning".
func runDoctorChecks(sn store.Snapshot) ([]store.DoctorCheck, string) {
	if sn.Doctor == nil {
		return nil, ""
	}
	var out []store.DoctorCheck
	failed, warned := 0, 0
	for _, c := range sn.Doctor.Checks {
		switch c.Status {
		case doctor.StatusFailed:
			failed++
		case doctor.StatusWarning:
			warned++
		default:
			continue
		}
		out = append(out, c)
	}
	var parts []string
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", failed))
	}
	if warned > 0 {
		parts = append(parts, pgPlural(warned, "warning", "warnings"))
	}
	if len(parts) == 0 {
		return nil, ""
	}
	return out, "Doctor: " + strings.Join(parts, ", ")
}

// runFailOutcome builds the outcome of a run that failed, or has a target
// that failed while it goes on, or was stopped, from the panel of what
// failed.
func runFailOutcome(rc runCtx, p *runFailPanel) *runOutcome {
	run := runRevealed(rc.Run, rc.Now)
	e := runEntryOf(rc.Snap, run)
	o := &runOutcome{Class: "bad", Mark: runMark(runFailed), Fail: p, Realm: e.realm}
	stopped := run.Done() && run.Status == store.StatusCanceled
	switch {
	case !run.Done():
		o.Title = "What failed"
	case stopped:
		o.Class, o.Mark, o.Title = "warn", runMark(runCanceled), "Stopped"
	case run.Root.Name == "create cluster" && e.cluster != "":
		o.Title = e.cluster + " was not created"
	default:
		o.Title = cmp.Or(run.Title, run.ID) + " failed"
	}
	tos := runTimeouts(run)
	var meta []string
	switch d := runLongestWait(tos); {
	case len(tos) > 0 && d > 0:
		meta = append(meta, runFoldNodes(runTimeoutNodes(tos))+" ran out of the "+runWaitWord(d)+" wait")
	case len(tos) > 0:
		meta = append(meta, runFoldNodes(runTimeoutNodes(tos))+" timed out")
	case stopped:
		meta = append(meta, "stopped after "+runDur(runElapsed(run, rc.Now)))
	case run.Done():
		meta = append(meta, "failed after "+runDur(runElapsed(run, rc.Now)))
	}
	if rb := runRollbackWord(run); rb != "" {
		meta = append(meta, rb)
	}
	o.Meta = strings.Join(meta, " · ")
	p.Says = runTimeoutSays(tos)
	if len(tos) > 0 {
		o.WhyTitle = "Why this often happens"
		o.Why = []string{
			"The host is busy or short of memory, so the nodes start slowly. A longer wait gives them the time.",
			"The host lacks something that the nodes need, such as cgroup v2 or enough inotify watches. Doctor checks the host.",
		}
	}
	if stopped {
		return o
	}
	o.Doctor, o.DoctorText = runDoctorChecks(rc.Snap)
	doc := pgAction{Label: "Check the host in Doctor", URL: "/doctor"}
	if slices.ContainsFunc(o.Doctor, func(c store.DoctorCheck) bool { return c.Status == doctor.StatusFailed }) {
		doc.Label = "Fix the host in Doctor"
	}
	o.Next = []pgAction{doc}
	o.CopyErr = cmp.Or(run.Err, runFailure(run))
	if h := runHeadOf(rc); h.Edit != nil || h.RetryID != "" {
		o.NextNote = "Try again, at the top, opens the form of this run."
	}
	return o
}

// runTimeoutNodes returns the nodes that ran out of time.
func runTimeoutNodes(tos []runTimeout) []string {
	out := make([]string, len(tos))
	for i, to := range tos {
		out[i] = to.Node
	}
	return out
}
