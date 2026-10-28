// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// The fixes of a node that needs a look: one rule set (pgLadder) that
// every place that shows the problem uses, the node page as "Try in this
// order", and Needs you, Ready for jobs, the Nodes tab and the Slurm tab
// with its first fix. The logs that tell the most about the problem
// (pgNode.LogsLink) are the same everywhere too.

// pgRung is a fix of the ladder of a node.
type pgRung struct {
	// Title names the fix, as "Restart slurmd"; Text says what it does.
	Title, Text string
	// Cmd is the command that the fix runs, as a terminal runs it.
	Cmd string
	// Act opens its dialog.
	Act pgAction
	// N is its place in the ladder, from 1; First is set on the first.
	N     int
	First bool
}

// Button is the button of the fix: the first one is the primary button
// of its page.
func (r pgRung) Button() pgAction {
	if r.First {
		r.Act.Class = "primary"
	}
	return r.Act
}

// pgLadder returns the fixes of a node that needs a look (pgNode.Problem),
// the one that most likely fixes it first: for a service that is down a
// restart of that service, after one of munge when munge is down too, then
// a reboot, or only a reboot while sshd is down (CanRestart); Start for a stopped node;
// Unfreeze for a paused one; Resume for a worker that Slurm sends no jobs.
// A node in a state that sind does not know, or one that needs no look,
// has none.
func pgLadder(n pgNode) []pgRung {
	l := pgRungs(n)
	for i := range l {
		l[i].N, l[i].First = i+1, i == 0
	}
	return l
}

// pgRungs returns the fixes of pgLadder, not yet numbered.
func pgRungs(n pgNode) []pgRung {
	if !n.Problem() {
		return nil
	}
	sind, arg := pgSind(n.Realm), n.Arg
	rung := func(title, text, cmd, verb, op string) pgRung {
		a := n.power(verb+"…", "", op)
		a.Name, a.Verb = verb+" "+arg+"…", verb
		return pgRung{Title: title, Text: text, Cmd: cmd, Act: a}
	}
	switch {
	case n.State == cluster.StatePaused:
		return []pgRung{rung("Unfreeze "+n.Short, "Its processes run on where they stopped.", sind+" power unfreeze "+arg, "Unfreeze", "unpause")}
	case n.State == cluster.StateStopped:
		return []pgRung{rung("Start "+n.Short, "Starts the container. systemd then starts its services.", sind+" power on "+arg, "Start", "start")}
	case n.State != cluster.StateRunning:
		return nil
	case len(n.Down()) > 0:
		reboot := rung("Reboot "+n.Short, "Stops, then starts the container. Use it when a restart does not help.", sind+" power reboot "+arg, "Reboot", "reboot")
		if !n.CanRestart() {
			reboot.Text = "Stops, then starts the container, and systemd starts sshd again. sind ssh needs sshd, so no service of " + n.Short + " can restart alone."
			return []pgRung{reboot}
		}
		restart := func(unit, text string) pgRung {
			return pgRung{Title: "Restart " + unit, Text: text, Cmd: sind + " ssh " + arg + " -- systemctl restart " + unit, Act: n.RestartAction(unit, "")}
		}
		unit := n.FailedUnit()
		var l []pgRung
		text := "Restarts one service. The other processes of " + n.Short + " go on."
		if unit != "munge" && slices.Contains(n.Down(), "munge") {
			// The Slurm daemons need munge: restarting one does not start
			// it.
			l = append(l, restart("munge", unit+" needs munge, so munge comes first. The other processes of "+n.Short+" go on."))
			text = "Restarts " + unit + " once munge runs."
		}
		return append(l, restart(unit, text), reboot)
	}
	return []pgRung{rung("Resume "+n.Short, "Slurm starts jobs on it again.",
		sind+" exec"+pgClusterArg(n.Cluster)+" -- scontrol update NodeName="+n.Short+" State=RESUME", "Resume", "resume")}
}

// pgFix returns the first fix of a node (pgLadder) as a button of the
// class, nil for none.
func pgFix(n pgNode, class string) *pgAction {
	l := pgLadder(n)
	if len(l) == 0 {
		return nil
	}
	a := l[0].Act
	a.Class = class
	return &a
}

// RowFix is the first fix of a node in a row of a table, nil for none.
func (n pgNode) RowFix() *pgAction { return pgFix(n, "sm") }

// CanRestart reports whether sind ui can restart a service of the node
// alone: it runs, and sshd is not down, as sind ssh logs in through it.
func (n pgNode) CanRestart() bool {
	return n.Running() && !slices.Contains(n.Down(), "sshd")
}

// RestartAction returns the dialog that restarts the unit of the node, as
// a button of the class.
func (n pgNode) RestartAction(unit, class string) pgAction {
	a := pgDialogAction("Restart "+unit+"…", class, "restart-service", "realm", n.Realm, "cluster", n.Cluster, "nodes", n.Short, "unit", unit)
	a.Name, a.Verb = "Restart "+unit+" on "+n.Arg+"…", "Restart"
	return a
}

// FailedUnit returns the service of a running node that tells the most
// about its problem: its Slurm service when that is down, else the first
// service that is down; empty for none.
func (n pgNode) FailedUnit() string {
	down := n.Down()
	if len(down) == 0 {
		return ""
	}
	if svc := pgServiceOf(n.Role); slices.Contains(down, svc) {
		return svc
	}
	return down[0]
}

// LogUnit returns the unit whose journal tells the most about the node:
// the service that failed (FailedUnit), else its Slurm service while it
// runs, unless the status says that it has none; empty for the output of
// its container, which Docker keeps for a stopped container too.
func (n pgNode) LogUnit() string {
	if !n.Running() {
		return ""
	}
	if u := n.FailedUnit(); u != "" {
		return u
	}
	svc := pgServiceOf(n.Role)
	if _, ok := n.Service(svc); !ok && len(n.Services) > 0 {
		return ""
	}
	return svc
}

// LogsLink returns the address of the Logs tab of the node's page with the
// log that tells the most about it (LogUnit). Every Logs link of a node
// leads there, so that the reader stays with the node and its actions.
func (n pgNode) LogsLink() string { return pgNodeLogsURL(n.Realm, n.Cluster, n.Short, n.LogUnit()) }

// LogsLabel labels the link to the node's log: "Logs of slurmd" for the
// journal of a unit, else "Logs".
func (n pgNode) LogsLabel() string {
	if u := n.LogUnit(); u != "" {
		return "Logs of " + u
	}
	return "Logs"
}

// LogsName names the link to the node's log for a screen reader, with the
// node: "Logs of slurmd on worker-4.dev", "Logs of worker-1.dev".
func (n pgNode) LogsName() string {
	if u := n.LogUnit(); u != "" {
		return "Logs of " + u + " on " + n.Arg
	}
	return "Logs of " + n.Arg
}

// LogsAction is the link to the node's log as a small button, labelled
// label.
func (n pgNode) LogsAction(label string) pgAction {
	return pgAction{Label: label, URL: n.LogsLink(), Class: "sm ghost", Name: n.LogsName()}
}

// RowLogs is the link to the node's log in a row beside its fix, nil for
// a worker that Slurm drained while its services run: its journal does not
// say why someone drained it, the reason in Slurm does.
func (n pgNode) RowLogs() *pgAction {
	if i, ok := n.SlurmIssue(); ok && strings.HasPrefix(i.Text, "drain") {
		return nil
	}
	a := n.LogsAction(n.LogsLabel())
	return &a
}

// pgNodeLogsURL returns the address of the Logs tab of a node page: the
// journal of unit, or the output of its container for no unit.
func pgNodeLogsURL(realm, name, short, unit string) string {
	q := url.Values{"tab": {"logs"}}
	if unit != "" {
		q.Set("source", string(logsJournal))
		q.Set("unit", unit)
	}
	return pgNodeURL(realm, name, short) + "?" + q.Encode()
}

// pgAttachFaults gives each node what sind ui read of why it needs a look.
func pgAttachFaults(sn store.Snapshot, nodes []pgNode) {
	for i, n := range nodes {
		if f, ok := sn.Fault(n.Realm, n.Cluster, n.Short); ok {
			nodes[i].Fault = &f
		}
	}
}

// Unit returns what sind ui read of a unit of the node that is down.
func (n pgNode) Unit(name string) (store.Unit, bool) {
	if n.Fault == nil {
		return store.Unit{}, false
	}
	return n.Fault.Unit(name)
}

// Exit returns how the node's container stopped, nil when not known or
// when it runs.
func (n pgNode) Exit() *store.Exit {
	if n.Fault == nil || n.Running() {
		return nil
	}
	return n.Fault.Exit
}

// Cause says in a few words why the node takes no jobs, beside the pill of
// its issue: since when its service is down ("since 09:41"), how its
// container stopped ("exit code 137, out of memory"), or the reason of
// Slurm ("memory errors"); empty when sind ui knows none.
func (n pgNode) Cause() string {
	if u, ok := n.Unit(n.FailedUnit()); ok && !u.Since.IsZero() {
		return "since " + pgHM(u.Since)
	}
	if e := n.Exit(); e != nil {
		return pgExitShort(*e)
	}
	return n.SlurmReason()
}

// pgExitShort says how a container stopped in a few words: "exit code 137,
// out of memory".
func pgExitShort(e store.Exit) string {
	s := fmt.Sprintf("exit code %d", e.Code)
	if e.OOMKilled {
		s += ", out of memory"
	}
	return s
}

// pgExitText says in a sentence what the exit code of a container means.
func pgExitText(e store.Exit) string {
	switch {
	case e.OOMKilled:
		return "the kernel killed it, as it ran out of memory"
	case e.Code == 0:
		return "its main process ended, as after a shutdown"
	case e.Code == 137:
		return "it was killed (SIGKILL), as by Cut power or docker kill"
	case e.Code == 143:
		return "it was stopped (SIGTERM), as by docker stop"
	}
	return "its main process failed"
}

// pgHM is a time of today as the pages write it in a sentence: "09:41".
func pgHM(t time.Time) string { return t.Local().Format("15:04") }

// pgHMS is a time to the second: "09:41:07".
func pgHMS(t time.Time) string { return t.Local().Format("15:04:05") }

// pgUnitRoles say what each service does, for the card of a node whose
// service failed.
var pgUnitRoles = map[string]string{
	"slurmd":     "the Slurm daemon of a worker: it starts and watches the jobs",
	"slurmctld":  "the Slurm controller: it schedules the jobs",
	"slurmdbd":   "the Slurm accounting daemon: it writes what sacct reads",
	"mariadb":    "the database of the accounting",
	"slurmrestd": "the Slurm REST API",
	"sackd":      "the auth daemon of the submitter: sbatch and squeue need it",
	"munge":      "the authentication of Slurm: every Slurm daemon of the node needs it",
	"sshd":       "the SSH server of the node: sind ssh needs it",
}

// pgUnitRole says what a service does, or that it is a service.
func pgUnitRole(unit string) string {
	if r, ok := pgUnitRoles[unit]; ok {
		return r
	}
	return "a service of the node"
}

// schedules reports whether Slurm runs jobs on the node: a worker that sind
// manages, or one whose status sind ui has not read yet.
func (n pgNode) schedules() bool {
	return n.Role == config.RoleWorker && (n.Managed || !n.Known)
}

// SlurmActions are the buttons of the node's row in the Slurm tab: for a
// node that needs a look its log and its first fix, as Reboot or Resume;
// for a worker that takes jobs Drain; none while a command creates or
// deletes it.
func (n pgNode) SlurmActions() []pgAction {
	if n.Life() {
		return nil
	}
	if fix := n.RowFix(); fix != nil {
		if logs := n.RowLogs(); logs != nil {
			return []pgAction{*logs, *fix}
		}
		return []pgAction{*fix}
	}
	if n.Worker() && n.Slurm != nil && slurm.NodeStateTakesJobs(n.Slurm.State) {
		a := n.power("Drain…", "sm ghost", "drain")
		a.Name = "Drain " + n.Arg + "…"
		return []pgAction{a}
	}
	return nil
}
