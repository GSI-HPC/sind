// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/GSI-HPC/go-clikit/doctor"
	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
)

// "Needs you", the inbox of the Overview: a row for each thing that needs
// the user, with what it is about, what it means for the jobs, and the one
// action that most likely fixes it in a column of its own. Work that goes
// on is no problem: it shows in the strip "Running now" at its end. A row
// whose problem a command fixed turns green, "Fixed", for a while.

// pgNeed is a row of "Needs you".
type pgNeed struct {
	// Key names the row, so that a live update keeps it in place.
	Key string
	// Sev is bad, warn, or ok for a row that is fixed.
	Sev string
	// Kind is what the row is about, one of the pgNeed constants; the head
	// of the Overview counts the rows by it. Label names it in a word, as
	// "Node", in the row's first column.
	Kind, Label string
	// Subject is what the row is about, as "worker-4.dev", and Title what
	// is wrong with it, as "has slurmd down".
	Subject, Title string
	// Detail says what is wrong, Effect what it means for the jobs, as
	// "Takes no jobs. dev runs jobs on 3 of 4 workers.".
	Detail, Effect string
	// Meta is the realm, when the Overview shows every realm.
	Meta string
	// Actions help to find out more, such as the logs; Fix is the one that
	// most likely fixes it, nil for none.
	Actions []pgAction
	Fix     *pgAction
	// Dismiss is the run that the row's × dismisses, empty for none.
	Dismiss string
	// HideAt is when the page hides a row that is fixed, in milliseconds
	// since 1970; 0 for a row that stays.
	HideAt int64
}

// The kinds of the rows of "Needs you".
const (
	pgNeedDocker  = "docker"
	pgNeedLock    = "lock"
	pgNeedStopped = "stopped"
	pgNeedPaused  = "paused"
	pgNeedUnknown = "unknown"
	pgNeedService = "service"
	pgNeedSlurm   = "slurm"
	pgNeedRun     = "run"
	pgNeedFailed  = "check-failed"
	pgNeedWarning = "check-warning"
	pgNeedFixed   = "fixed"
)

// fixedFor is how long a row that a command fixed stays, green.
const fixedFor = 30 * time.Second

// pgNeedOrder is the order of the rows by kind: what stops everything
// first, then the nodes, the runs, the host and a stale lock; the rows
// that are fixed last.
var pgNeedOrder = []string{pgNeedDocker, pgNeedService, pgNeedStopped, pgNeedSlurm, pgNeedPaused, pgNeedUnknown, pgNeedRun, pgNeedFailed, pgNeedWarning, pgNeedLock, pgNeedFixed}

// ID is the id of the row's element, from its key.
func (n pgNeed) ID() string { return "need-" + strings.NewReplacer("/", "-", " ", "-").Replace(n.Key) }

// pgNowRun is a command that runs now, in the strip "Running now".
type pgNowRun struct {
	pgRun
	// Line says what it does now; Lock says how it stands with the lock of
	// its realm, as "holds the lock of ci".
	Line, Lock string
	// Elapsed and Since are its clock.
	Elapsed string
	Since   int64
}

// pgNeedsOf fills the rows of "Needs you" and the strip "Running now" of
// the Overview, in the realm or in all realms (realm empty).
func pgNeedsOf(d *pgOverview, sn store.Snapshot, now time.Time) {
	realm := d.Realm
	var out []pgNeed
	if sn.Docker.Checked && sn.Docker.Err != "" {
		out = append(out, pgNeed{
			Key: "docker", Sev: "bad", Kind: pgNeedDocker, Label: "Docker", Subject: "Docker", Title: "does not answer",
			Detail: sn.Docker.Err + ".", Effect: "The pages show what sind ui read last, and no action works.",
			Fix: &pgAction{Label: "Open Doctor", URL: "/doctor", Class: "sm"},
		})
	}
	// A command that runs past the wait for the lock of its realm holds
	// it, and explains the lock; one that waits for the lock cannot hold
	// it. A command whose realm is unknown may hold any lock.
	holds := map[string]bool{}
	waits := map[string][]string{}
	unknown := false
	for _, r := range sn.Runs {
		if r.Done() {
			continue
		}
		e := runEntryOf(sn, r)
		for _, x := range e.realms {
			if runWaitsForLock(r) {
				waits[x] = append(waits[x], e.title)
			} else {
				holds[x] = true
			}
		}
		unknown = unknown || len(e.realms) == 0 && !runWaitsForLock(r)
		if realm == "" || slices.Contains(e.realms, realm) {
			d.Running = append(d.Running, pgNowRunOf(sn, r, e, now))
		}
	}
	slices.Reverse(d.Running)
	for _, r := range pgRealmNames(sn, realm) {
		meta := ""
		if realm == "" {
			meta = r
		}
		if h := sn.Locks[r]; h != nil && !holds[r] && !unknown {
			effect := "Commands on this realm wait for the lock."
			if w := waits[r]; len(w) == 1 {
				effect = w[0] + " waits for it."
			} else if len(w) > 1 {
				effect = pgJoin(w) + " wait for it."
			}
			out = append(out, pgNeed{
				Key: "lock/" + r, Sev: "warn", Kind: pgNeedLock, Label: "Lock", Subject: "realm " + r, Title: "stays locked, but no command runs",
				Detail: h.String() + ". The command may have been killed.", Effect: effect,
				Actions: []pgAction{{Label: "Show activity", URL: "/activity", Class: "sm ghost"}}, Meta: meta,
			})
		}
		nodes := pgSnapNodes(sn, r, now)
		for _, n := range nodes {
			if n.Problem() {
				need := pgNodeNeed(sn, n, nodes)
				need.Meta = meta
				out = append(out, need)
			}
		}
		out = append(out, pgFixedNodes(sn, r, nodes, meta, now)...)
	}
	for _, f := range pgFailures(sn, now) {
		e := runEntryOf(sn, f.Run)
		if realm != "" && !slices.Contains(e.realms, realm) {
			continue
		}
		need := pgRunNeed(sn, f, e, now)
		if realm != "" {
			need.Meta = ""
		}
		out = append(out, need)
	}
	if sn.Doctor != nil {
		for _, c := range sn.Doctor.Checks {
			if c.Status == doctor.StatusOK {
				continue
			}
			need := pgNeed{
				Key: "doctor/" + c.Name, Sev: "warn", Kind: pgNeedWarning, Label: "Host", Subject: c.Name, Title: "warns",
				Detail: c.Detail, Fix: &pgAction{Label: "Show the fix", URL: "/doctor", Class: "sm"},
			}
			if c.Status == doctor.StatusFailed {
				need.Sev, need.Kind, need.Title = "bad", pgNeedFailed, "check failed"
				need.Effect = "sind creates no cluster and starts no node until it passes."
			}
			out = append(out, need)
		}
	}
	slices.SortStableFunc(out, func(a, b pgNeed) int {
		return slices.Index(pgNeedOrder, a.Kind) - slices.Index(pgNeedOrder, b.Kind)
	})
	d.Needs = out
}

// pgNowRunOf builds a command of the strip "Running now".
func pgNowRunOf(sn store.Snapshot, r store.Run, e runEntry, now time.Time) pgNowRun {
	shown := runRevealed(r, now)
	var task *store.Task
	if t, ok := runTaskOf(sn, r.Trace); ok {
		task = &t
	}
	nr := pgNowRun{pgRun: pgRunOf(sn, r, now), Line: runLine(shown, task, now), Elapsed: runDur(runElapsed(r, now)), Since: runSince(r)}
	if e.realm != "" {
		nr.Lock = "holds the lock of realm " + e.realm
		if nr.Waiting {
			nr.Lock = "waits for the lock of realm " + e.realm
		}
	}
	return nr
}

// pgNodeNeed describes a node that needs a look (pgNode.Problem): one that
// does not run, runs with a service down, or that Slurm sends no jobs; with
// what it means for the jobs of its cluster, whose nodes are nodes.
func pgNodeNeed(sn store.Snapshot, n pgNode, nodes []pgNode) pgNeed {
	need := pgNeed{
		Key: "node/" + n.Realm + "/" + n.Arg, Sev: "warn", Kind: pgNeedUnknown, Label: "Node", Subject: n.Arg,
		Title: "is in a state that sind does not know", Effect: pgNodeEffect(n, nodes),
	}
	cs := "unknown"
	if c, ok := sn.Cluster(n.Realm, n.Cluster); ok {
		cs = string(c.State)
	}
	logs := pgAction{Label: "Logs", URL: n.LogsLink(), Class: "sm ghost"}
	fix := func(verb, op string) *pgAction {
		a := n.power(verb+" "+n.Short+"…", "sm", op)
		a.Name = verb + " " + n.Arg + "…"
		return &a
	}
	switch {
	case n.State == cluster.StateRunning && len(n.Down()) == 0:
		// Slurm sends the node no jobs (pgNode.SlurmIssue).
		need.Kind, need.Label = pgNeedSlurm, "Slurm"
		need.Title = "is " + n.Slurm.State + " in Slurm"
		need.Detail = "Slurm sends it no new jobs."
		if n.Slurm.Reason != "" {
			need.Detail += " Reason: " + n.Slurm.Reason + "."
		}
		need.Actions = []pgAction{{Label: "Logs of slurmd", URL: pgLogsURL(n.Realm, n.Cluster, n.Short, "slurmd"), Class: "sm ghost"}}
		need.Fix = fix("Resume", "resume")
	case n.State == cluster.StateRunning:
		down := n.Down()
		need.Sev, need.Kind = "bad", pgNeedService
		need.Title = "has " + strings.Join(down, ", ") + " down"
		verb := "is"
		if len(down) > 1 {
			verb = "are"
		}
		need.Detail = fmt.Sprintf("The container runs, but %s %s not active.", pgJoin(down), verb)
		unit := down[0]
		if svc := pgServiceOf(n.Role); slices.Contains(down, svc) {
			unit = svc
		}
		need.Actions = []pgAction{{Label: "Logs of " + unit, URL: pgLogsURL(n.Realm, n.Cluster, n.Short, unit), Class: "sm ghost"}}
		need.Fix = fix("Reboot", "reboot")
	case n.State == cluster.StateStopped:
		need.Sev, need.Kind, need.Title = "bad", pgNeedStopped, "is stopped"
		need.Detail = fmt.Sprintf("The container does not run. The cluster %s is %s.", n.Cluster, cs)
		need.Actions = []pgAction{logs}
		need.Fix = fix("Start", "start")
	case n.State == cluster.StatePaused:
		need.Kind, need.Title = pgNeedPaused, "is paused"
		need.Detail = fmt.Sprintf("The container is paused. The cluster %s is %s.", n.Cluster, cs)
		need.Actions = []pgAction{logs}
		need.Fix = fix("Unfreeze", "unpause")
	default:
		need.Detail = fmt.Sprintf("Docker reports a state that sind does not know. The cluster %s is %s.", n.Cluster, cs)
		need.Actions = []pgAction{logs}
	}
	return need
}

// pgNodeEffect says what a node that needs a look means for the jobs of
// its cluster, whose nodes are nodes: a worker takes none, and the cluster
// runs jobs on fewer workers; a controller, db, api or submitter node has
// a sentence of its own.
func pgNodeEffect(n pgNode, nodes []pgNode) string {
	switch n.Role {
	case config.RoleWorker:
	case config.RoleController:
		for _, o := range nodes {
			if o.Cluster == n.Cluster && o.Role == config.RoleController && o.Short != n.Short && pgWhyNot(o, "slurmctld") == "" {
				return "The other controller schedules the jobs."
			}
		}
		return "No new job starts on " + n.Cluster + "."
	case config.RoleDB:
		return "Jobs run, but sacct does not record them."
	case config.RoleAPI:
		return "slurmrestd does not answer."
	default:
		return "You cannot submit jobs from it."
	}
	effect := "Takes no jobs."
	if n.Slurm != nil && n.Slurm.State == "draining" {
		effect = "Takes no new jobs, and ends the ones it runs."
	}
	took, of := 0, 0
	for _, o := range nodes {
		if o.Cluster != n.Cluster || o.Role != config.RoleWorker || !o.Managed && o.Known {
			continue
		}
		of++
		if pgWhyNot(o, "slurmd") == "" {
			took++
		}
	}
	if took == 0 {
		return effect + " No worker of " + n.Cluster + " takes jobs."
	}
	return effect + fmt.Sprintf(" %s runs jobs on %d of %d workers.", n.Cluster, took, of)
}

// pgFailure is a run that failed lately, with the newer run of the same
// command that ended ok, if one did.
type pgFailure struct {
	Run store.Run
	// Fixed is the newer run of the same command that ended ok, nil for
	// none.
	Fixed *store.Run
}

// pgFailures returns the runs that failed within failedWindow and that
// still need the user, newest first: not dismissed, and not followed by a
// run of the same command in the same realm that ended ok, unless that one
// ended within fixedFor, which makes the row of the failure a row that is
// fixed. The rail counts the ones without Fixed.
func pgFailures(sn store.Snapshot, now time.Time) []pgFailure {
	var out []pgFailure
	for i, r := range sn.Runs {
		if !r.Done() || r.Status != store.StatusFailed || now.Sub(*r.End) >= failedWindow || sn.Dismissed[r.ID] {
			continue
		}
		f := pgFailure{Run: r}
		realms := runEntryOf(sn, r).realms
		if j := slices.IndexFunc(sn.Runs[:i], func(o store.Run) bool {
			return o.Done() && o.Status == store.StatusOK && o.Title == r.Title && pgSameRealm(realms, runEntryOf(sn, o).realms)
		}); j >= 0 {
			if now.Sub(*sn.Runs[j].End) >= fixedFor {
				continue
			}
			f.Fixed = &sn.Runs[j]
		}
		out = append(out, f)
	}
	return out
}

// pgSameRealm reports whether two runs may have worked in the same realm,
// given the realms that each may have worked on (runEntry): a run whose
// realm is not known may have worked in any.
func pgSameRealm(a, b []string) bool {
	return len(a) == 0 || len(b) == 0 || slices.ContainsFunc(a, func(r string) bool { return slices.Contains(b, r) })
}

// pgRunNeed describes a run that failed, or a row that is fixed once a
// newer run of its command ended ok.
func pgRunNeed(sn store.Snapshot, f pgFailure, e runEntry, now time.Time) pgNeed {
	r := f.Run
	need := pgNeed{
		Key: "run/" + r.ID, Sev: "bad", Kind: pgNeedRun, Label: "Run", Subject: e.title, Title: "failed " + pgAgo(*r.End, now),
		Meta: e.realm, Dismiss: r.ID,
	}
	need.Detail, _, _ = strings.Cut(strings.TrimSpace(runFailure(r)), "\n")
	show := pgAction{Label: "Show what failed", URL: pgRun{ID: r.ID}.Link(), Class: "sm ghost"}
	if f.Fixed != nil {
		need.Sev, need.Kind, need.Label, need.Dismiss = "ok", pgNeedFixed, "Fixed", ""
		need.Title, need.Detail = "is fixed", "A newer run of the same command ended ok "+pgAgo(*f.Fixed.End, now)+"."
		need.Actions = []pgAction{{Label: "Show the run", URL: pgRun{ID: f.Fixed.ID}.Link(), Class: "sm ghost"}}
		need.HideAt = f.Fixed.End.Add(fixedFor).UnixMilli()
		return need
	}
	need.Fix = &show
	// Try again opens the dialog of the command with its form, as the run
	// page does: the task's, or for a run from a terminal the dialog of
	// create cluster or create worker with what the log names.
	var edit *runLink
	if t, ok := runTaskOf(sn, r.Trace); ok {
		_, edit = runRetry(t, runFailed)
	} else {
		h := &runHead{}
		runHeadNoTask(runCtx{Snap: sn, Now: now, Run: r}, h)
		edit = h.Edit
	}
	if edit != nil {
		need.Fix = &pgAction{Label: "Try again…", URL: edit.Href, Class: "sm", Dialog: true}
		need.Actions = []pgAction{show}
	}
	return need
}

// pgFixOps are the ops of the power dialog that bring a node back.
var pgFixOps = []string{"start", "on", "reboot", "restart", "cycle", "unpause", "unfreeze", "resume"}

// pgFixes returns the nodes, by short name, that a command of sind ui may
// fix: those that a power command that brings nodes back names, and that
// need a look in the snapshot, as it starts. Its task keeps them (Fixes).
func pgFixes(sn store.Snapshot, t store.Task, now time.Time) []string {
	if t.Action != "power" || !slices.Contains(pgFixOps, pgFormValue(t.Form, "op")) {
		return nil
	}
	names := action.NodeNames(t.Form)
	var out []string
	for _, n := range pgSnapNodes(sn, t.Realm, now) {
		if n.Cluster == t.Cluster && slices.Contains(names, n.Short) && n.Problem() {
			out = append(out, n.Short)
		}
	}
	return out
}

// pgFixedNodes returns a row that is fixed for each node of the realm that
// needed a look when a command of sind ui that brings it back started
// (pgFixes), that the command ended ok within fixedFor, and that needs no
// look now: the row of its problem turns green for a while. Of several
// such commands, the newest names the row.
func pgFixedNodes(sn store.Snapshot, realm string, nodes []pgNode, meta string, now time.Time) []pgNeed {
	var out []pgNeed
	for _, t := range slices.Backward(sn.Tasks) {
		if t.Status != store.TaskOK || t.End == nil || now.Sub(*t.End) >= fixedFor || t.Realm != realm {
			continue
		}
		for _, short := range t.Fixes {
			i := slices.IndexFunc(nodes, func(n pgNode) bool { return n.Cluster == t.Cluster && n.Short == short })
			if i < 0 || nodes[i].Problem() {
				continue
			}
			n := nodes[i]
			key := "node/" + n.Realm + "/" + n.Arg
			if slices.ContainsFunc(out, func(o pgNeed) bool { return o.Key == key }) {
				continue
			}
			out = append(out, pgNeed{
				Key: key, Sev: "ok", Kind: pgNeedFixed, Label: "Fixed", Subject: n.Arg, Title: "is fixed",
				Detail: pgOr(t.Display, "The command") + " ended ok " + pgAgo(*t.End, now) + ".",
				Meta:   meta, HideAt: t.End.Add(fixedFor).UnixMilli(),
				Actions: []pgAction{{Label: "Open " + n.Short, URL: n.Link(), Class: "sm ghost"}},
			})
		}
	}
	return out
}

// pgFormValue returns the first value of key in a task's form.
func pgFormValue(form map[string][]string, key string) string {
	if v := form[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// NeedsText says how many rows are open and how many fixed: "3 open · 1
// fixed". run.js says it again as it hides the rows that are fixed.
func (d *pgOverview) NeedsText() string {
	open := d.OpenCount()
	text := fmt.Sprintf("%d open", open)
	if fixed := len(d.Needs) - open; fixed > 0 {
		text += fmt.Sprintf(" · %d fixed", fixed)
	}
	return text
}

// OpenCount counts the rows that need the user, not those that are fixed.
func (d *pgOverview) OpenCount() int {
	return pgCount(d.Needs, func(n pgNeed) bool { return n.Kind != pgNeedFixed })
}

// RunningLink returns the address of Activity with the runs that go on, in
// the realm of the Overview.
func (d *pgOverview) RunningLink() string { return runQuery{Status: "running", Realm: d.Realm}.href() }

// Open reports whether a row needs the user, not one that is fixed.
func (d *pgOverview) Open() bool { return d.OpenCount() > 0 }

// pgDismissRun answers POST /needs/runs/{id}/dismiss: the run that failed
// leaves "Needs you", for as long as sind ui runs. Its run page and
// Activity keep it.
func pgDismissRun(s *Server, w http.ResponseWriter, r *http.Request) {
	v, err := s.View(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	s.env.Store.DismissRun(v.Params.Get("id"))
	w.WriteHeader(http.StatusNoContent)
}
