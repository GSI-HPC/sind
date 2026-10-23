// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// The "Ready for jobs" card of a cluster's Summary: a verdict on whether
// the cluster can run a job now, the checks behind it, and the first
// commands.

// pgReadyCheck is one check of the card.
type pgReadyCheck struct {
	// State is ok, warn, bad, or none for a check that does not apply.
	State   string
	Title   string
	Detail  string
	Actions []pgAction
}

// Mark returns the sign of the check's state.
func (c pgReadyCheck) Mark() string {
	switch c.State {
	case "ok":
		return "✓"
	case "warn":
		return "!"
	case "bad":
		return "✗"
	}
	return "–"
}

// Word returns the check's state in words, for screen readers.
func (c pgReadyCheck) Word() string {
	switch c.State {
	case "ok":
		return "passed"
	case "warn":
		return "warning"
	case "bad":
		return "failed"
	}
	return "does not apply"
}

// pgReady is the card.
type pgReady struct {
	// Checked is set once the cluster was probed; before, the card has
	// no checks.
	Checked bool
	// Verdict answers "can it run a job now?", Class colours it.
	Verdict, Class string
	Checks         []pgReadyCheck
	// Commands are the first two commands of a job, Note says where its
	// files go.
	Commands []pgCmd
	Note     string
}

// pgEntry returns the node that `sind enter` opens a shell on: the
// submitter when the cluster has one, else the controller.
func pgEntry(nodes []pgNode) string {
	for _, n := range nodes {
		if n.Role == config.RoleSubmitter {
			return "submitter"
		}
	}
	return "controller"
}

// pgPowerFix returns the power dialog that may fix a node that does not
// take jobs, and whether there is one: Start for a stopped node, Unfreeze
// for a paused one, Reboot for one with a service down.
func pgPowerFix(n pgNode) (pgAction, bool) {
	op, label := "reboot", "Reboot…"
	switch {
	case n.State == cluster.StatePaused:
		op, label = "unpause", "Unfreeze…"
	case n.State != cluster.StateRunning:
		op, label = "start", "Start…"
	case len(n.Down()) == 0:
		return pgAction{}, false
	}
	return pgDialogAction(label, "sm ghost", "power", "realm", n.Realm, "cluster", n.Cluster, "nodes", n.Short, "op", op), true
}

// pgFixActions returns the links that help with a node: its logs, and
// the power dialog that may fix it.
func pgFixActions(n pgNode) []pgAction {
	acts := []pgAction{{Label: "Logs of " + n.Short, URL: n.LogsLink(), Class: "sm ghost"}}
	if fix, ok := pgPowerFix(n); ok {
		acts = append(acts, fix)
	}
	return acts
}

// pgWhyNot says why a node does not run its service svc, or does not take
// jobs: "stopped", "slurmd is down", "drained (disk check)"; empty when
// nothing is wrong.
func pgWhyNot(n pgNode, svc string) string {
	if n.State != cluster.StateRunning {
		return string(n.State)
	}
	if up, ok := n.Service(svc); ok && !up {
		return svc + " is down"
	}
	if svc == "slurmd" && n.Slurm != nil && !slurm.NodeStateTakesJobs(n.Slurm.State) {
		if n.Slurm.Reason != "" {
			return n.Slurm.State + " in Slurm (" + n.Slurm.Reason + ")"
		}
		return n.Slurm.State + " in Slurm"
	}
	return ""
}

// pgReadyOf builds the card from the cluster's nodes, its data mount and
// what Slurm says.
func pgReadyOf(d *pgClusterView) *pgReady {
	r := &pgReady{Verdict: "not checked yet"}
	entry := pgEntry(d.Cluster.Nodes)
	data := cluster.DefaultDataMountPath
	if d.Data != nil {
		data = d.Data.Path
	}
	r.Commands = []pgCmd{
		{"Open a shell on the " + entry, fmt.Sprintf("%s enter%s", pgSind(d.Realm), pgClusterArg(d.Name))},
		{"In that shell, submit a test job and show the queue", "sbatch --chdir=" + data + " --wrap hostname && squeue"},
	}
	r.Note = "Put job scripts in " + data + ": every node sees it, and the output of the test job lands there."
	if d.Data != nil && d.Data.Type == config.StorageHostPath {
		r.Note = "Put job scripts in " + data + ": it is " + d.Data.Source + " on this host, and the output of the test job lands there."
	}
	if d.Status == nil {
		return r
	}
	r.Checked = true
	ctl := pgControllerCheck(d)
	workers, took, of := pgWorkersCheck(d)
	r.Checks = []pgReadyCheck{ctl, workers, pgAccountingCheck(d)}
	if files, ok := pgFilesCheck(d); ok {
		r.Checks = append(r.Checks, files)
	}
	switch {
	case ctl.State == "bad" || workers.State == "bad":
		r.Verdict, r.Class = "no", "bad"
	case workers.State == "warn":
		r.Verdict, r.Class = fmt.Sprintf("yes, with %d of %d workers", took, of), "warn"
	default:
		r.Verdict, r.Class = "yes", "ok"
		for _, c := range r.Checks[2:] {
			if c.State == "warn" || c.State == "bad" {
				r.Verdict, r.Class = "yes, but see below", "warn"
			}
		}
	}
	if r.Class != "ok" && slices.ContainsFunc(d.Cluster.Nodes, func(n pgNode) bool { return n.Creating }) {
		// A command creates the cluster: what is not ready may be starting,
		// and a fix would get in its way.
		r.Verdict, r.Class = "not yet: the cluster is being created", "run"
		for i := range r.Checks {
			r.Checks[i].Actions = nil
		}
	}
	return r
}

// pgControllerCheck checks that slurmctld runs on a controller and answers
// sinfo.
func pgControllerCheck(d *pgClusterView) pgReadyCheck {
	var ctls []pgNode
	for _, n := range d.Cluster.Nodes {
		if n.Role == config.RoleController {
			ctls = append(ctls, n)
		}
	}
	if len(ctls) == 0 {
		return pgReadyCheck{State: "bad", Title: "No controller", Detail: "The cluster has no controller node, so Slurm cannot schedule."}
	}
	if !ctls[0].Managed {
		return pgReadyCheck{State: "none", Title: "Controller", Detail: "sind does not manage Slurm on the controller, so it does not check slurmctld."}
	}
	var running, broken []string
	var first *pgNode
	for i, n := range ctls {
		if why := pgWhyNot(n, "slurmctld"); why != "" {
			broken = append(broken, n.Short+": "+why)
			if first == nil {
				first = &ctls[i]
			}
			continue
		}
		running = append(running, n.Short)
	}
	if len(running) == 0 {
		return pgReadyCheck{State: "bad", Title: "The controller does not answer", Detail: strings.Join(broken, "; ") + ".", Actions: pgFixActions(*first)}
	}
	if d.Slurm.Err != "" {
		return pgReadyCheck{State: "bad", Title: "The controller does not answer", Detail: "sinfo fails: " + d.Slurm.Err, Actions: pgFixActions(ctls[0])}
	}
	c := pgReadyCheck{State: "ok", Title: "The controller answers", Detail: "slurmctld runs on " + pgJoin(running) + "."}
	if len(broken) > 0 {
		c.State, c.Detail = "warn", c.Detail+" "+strings.Join(broken, "; ")+": no failover."
		c.Actions = pgFixActions(*first)
	}
	return c
}

// pgWorkersCheck counts the managed workers that take jobs: they run,
// slurmd is up, and Slurm schedules on them.
func pgWorkersCheck(d *pgClusterView) (c pgReadyCheck, took, of int) {
	var why []string
	var first *pgNode
	for i, n := range d.Cluster.Nodes {
		if n.Role != config.RoleWorker || !n.Managed {
			continue
		}
		of++
		if w := pgWhyNot(n, "slurmd"); w != "" {
			why = append(why, n.Short+": "+w)
			if first == nil {
				first = &d.Cluster.Nodes[i]
			}
			continue
		}
		took++
	}
	switch {
	case of == 0:
		return pgReadyCheck{State: "bad", Title: "No worker", Detail: "Slurm runs jobs on workers. Add one.",
			Actions: []pgAction{pgDialogAction("Add workers…", "sm ghost", "create-worker", "realm", d.Realm, "cluster", d.Name)}}, 0, 0
	case took == of && of == 1:
		return pgReadyCheck{State: "ok", Title: "The worker takes jobs"}, took, of
	case took == of:
		return pgReadyCheck{State: "ok", Title: fmt.Sprintf("All %d workers take jobs", of)}, took, of
	}
	c = pgReadyCheck{State: "warn", Title: fmt.Sprintf("%d of %d workers take jobs", took, of), Detail: strings.Join(why, "; ") + ".", Actions: pgFixActions(*first)}
	if took == 0 {
		c.State, c.Title = "bad", "No worker takes jobs"
	}
	return c, took, of
}

// pgAccountingCheck checks that slurmdbd and MariaDB run on the db node,
// when the cluster has one.
func pgAccountingCheck(d *pgClusterView) pgReadyCheck {
	for _, n := range d.Cluster.Nodes {
		if n.Role != config.RoleDB {
			continue
		}
		if !n.Managed {
			return pgReadyCheck{State: "none", Title: "Accounting", Detail: "sind does not manage Slurm on " + n.Short + ", so it does not check slurmdbd."}
		}
		var why []string
		for _, svc := range []string{"mariadb", "slurmdbd"} {
			if w := pgWhyNot(n, svc); w != "" && !slices.Contains(why, w) {
				why = append(why, w)
			}
		}
		if len(why) > 0 {
			return pgReadyCheck{State: "warn", Title: "Accounting is down", Detail: n.Short + ": " + strings.Join(why, ", ") + ". Jobs run, but sacct does not record them.", Actions: pgFixActions(n)}
		}
		return pgReadyCheck{State: "ok", Title: "Accounting works", Detail: "slurmdbd and MariaDB run on " + n.Short + "."}
	}
	return pgReadyCheck{State: "none", Title: "No accounting", Detail: "The cluster has no db node, so sacct has no records."}
}

// pgFilesCheck says where the files of jobs are, when the status names the
// data mount.
func pgFilesCheck(d *pgClusterView) (pgReadyCheck, bool) {
	m := d.Data
	switch {
	case m == nil:
		return pgReadyCheck{}, false
	case !m.OK:
		return pgReadyCheck{State: "bad", Title: "The data volume is missing", Detail: "Docker has no volume " + m.Source + ", so " + m.Path + " is empty."}, true
	case m.Type == config.StorageHostPath:
		return pgReadyCheck{State: "ok", Title: "Your files", Detail: m.Source + " on this host is " + m.Path + " on every node."}, true
	}
	return pgReadyCheck{State: "ok", Title: "Your files", Detail: "The volume " + m.Source + " is " + m.Path + " on every node."}, true
}
