// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/GSI-HPC/go-nodeset"
	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// The page of one node, a place to find out what is wrong with it and to
// fix it: a card that says what failed, when, what that means for the jobs
// and the end of the journal of the failed unit, with the fixes to try in
// order (pgLadder); the verbs that do not fix it in menus; what changed on
// it while sind ui ran; and its log in a tab of its own, so that the reader
// stays with the node and its actions.

// pgNodePage is the page of one node, template pg_node.tmpl.
var pgNodePage = Page{
	Name: "node", Pattern: "GET /nodes/{realm}/{cluster}/{node}", Title: "Node", Nav: "nodes",
	Params: []string{"tab"}, Fragments: []string{"pg-node-head", "pg-node-body"},
	Load: func(v *View) error {
		d := pgLoadNode(v)
		if !d.Found {
			return pgNodeMiss(v.Snap, d.Realm, d.Cluster, d.Short)
		}
		v.Title = d.Node.Arg
		if d.Tab == "logs" {
			v.Title = "Logs · " + d.Node.Arg
		}
		return nil
	},
}

func pgNodeFragments() []Fragment {
	params := []string{"realm", "cluster", "node", "tab"}
	load := func(v *View) error { v.Data = pgLoadNode(v); return nil }
	return []Fragment{
		{Name: "pg-node-head", Params: params, Watch: pgClusterWatch, Load: load},
		{
			// The Logs tab keeps no topic, as a new render of it would end
			// the stream that it holds, and reads its own keys.
			Name: "pg-node-body", Params: append(slices.Clone(params), logsTabParams...),
			Watch: func(p Params) []store.Topic {
				if p.Get("tab") == "logs" {
					return nil
				}
				return pgClusterWatch(p)
			},
			Load: load,
		},
	}
}

// pgNodeView is what a node page shows.
type pgNodeView struct {
	// Found is false when the snapshot knows no such node.
	Found                 bool
	Realm, Cluster, Short string
	Node                  pgNode
	// Tab is overview or logs, Tabs the links to both.
	Tab  string
	Tabs []pgTabLink
	// ClusterPill is the state pill of the node's cluster, as its pages
	// show it: "creating · nodes 1/3" while a command creates it.
	ClusterPill pgPill
	// Slurm is what Slurm says about the node's cluster.
	Slurm *pgSlurmView
	// Menus hold the verbs of the head, by kind: Slurm, Power, and ⋯ for
	// Delete worker; empty while a command creates or deletes the node.
	Menus    []pgMenu
	Commands []pgCmd
	// Run is the command that creates or deletes the node now, nil for
	// none; the node then has no actions.
	Run *pgRun
	// Works are the commands that work on the node now, such as a
	// reboot, for the line under the head.
	Works []pgNodeAct
	// Diag is the card of the node's problem, nil when it has none; Fixed
	// is the card that says that a command fixed it, nil for none.
	Diag  *pgDiag
	Fixed *pgFixed
	// Services are the rows of its services, and Changes what changed on
	// it while sind ui ran, the newest first.
	Services []pgSvcRow
	Changes  []pgChangeRow
}

// pgServiceOf returns the Slurm service that a role runs.
func pgServiceOf(r config.Role) string {
	switch r {
	case config.RoleController:
		return "slurmctld"
	case config.RoleDB:
		return "slurmdbd"
	case config.RoleAPI:
		return "slurmrestd"
	case config.RoleSubmitter:
		return "sackd"
	}
	return "slurmd"
}

// pgLoadNode builds the view of a node page from the view's Params (realm,
// cluster, node, tab).
func pgLoadNode(v *View) *pgNodeView {
	realm, name, short := v.Params.Get("realm"), v.Params.Get("cluster"), v.Params.Get("node")
	d := &pgNodeView{Realm: realm, Cluster: name, Short: short, Tab: "overview"}
	if v.Params.Get("tab") == "logs" {
		d.Tab = "logs"
	}
	container := string(cluster.ContainerName(realm, name, short))
	for _, ns := range v.Snap.ClusterNodes(realm, name) {
		if ns.Container != container {
			continue
		}
		d.Found = true
		d.Node = pgNodeOf(realm, ns, v.Snap.Status(realm, name))
		w := pgWorkOf(v.Snap, realm, name, v.Now)
		w.apply(&d.Node)
		if r, ok := w.runOf(short); ok && d.Node.Life() {
			d.Run = &r
		}
		nodes := []pgNode{d.Node}
		pgAttachSlurm(nodes, name, v.Snap.Slurm(realm, name))
		pgAttachFaults(v.Snap, nodes)
		d.Node = nodes[0]
		d.Slurm = pgSlurmOf(v.Snap.Slurm(realm, name), nodes)
	}
	if !d.Found {
		return d
	}
	if c, ok := v.Snap.Cluster(realm, name); ok {
		d.ClusterPill = pgClusterOf(v.Snap, realm, c, v.Snap.Monitorings[realm], v.Now).Pill()
	}
	n := d.Node
	d.Tabs = []pgTabLink{
		{Key: "overview", Label: "Overview", URL: n.Link(), On: d.Tab == "overview"},
		{Key: "logs", Label: "Logs", URL: n.LogsLink(), On: d.Tab == "logs"},
	}
	sind := pgSind(realm)
	d.Commands = []pgCmd{
		{"Log in to the node", fmt.Sprintf("%s ssh %s", sind, n.Arg)},
		{"Follow its journal", fmt.Sprintf("%s logs %s %s -f -n 200", sind, n.Arg, pgServiceOf(n.Role))},
	}
	acts := pgNodeActs(v.Snap, n, v.Now)
	for _, a := range acts {
		if a.Running {
			d.Works = append(d.Works, a)
		}
	}
	d.Services = pgSvcRows(v.Snap, n)
	d.Changes = pgChangeRows(v.Snap, n, acts, v.Now)
	if d.Run != nil {
		// A command creates or deletes the node: an action would get in
		// its way.
		return d
	}
	d.Menus = pgNodeMenus(n)
	d.Fixed, d.Diag = pgFixedOf(v.Snap, n, v.Now), pgDiagOf(v.Snap, n, acts, v.Now)
	switch {
	case d.Diag == nil || d.Fixed == nil:
	case d.Fixed.back:
		// sind ui saw the fix hold, and then the problem come back.
		d.Diag.Again = d.Fixed.What + " Since then, the problem came back."
		d.Fixed = nil
	default:
		// The status does not show the fix yet, as a service still
		// starts: the card of the fix waits, and the card of the problem
		// shows in its place once it hides.
		d.Fixed.Wait = true
		d.Fixed.Title = d.Fixed.waits
		d.Diag.Again = d.Fixed.What + " The problem is still there."
		d.Diag.ShowAt = d.Fixed.HideAt
	}
	return d
}

// LogsEnd says, on the Logs tab of a node that does not run, where its
// log ends; empty for a node that runs.
func (d *pgNodeView) LogsEnd() string {
	n := d.Node
	if n.Running() {
		return ""
	}
	where := "where it stopped"
	if e := n.Exit(); e != nil && !e.At.IsZero() {
		where = "at " + pgHMS(e.At) + ", where it stopped"
	}
	return "Its container output ends " + where + ", and its journal can be read only while it runs."
}

// LogsNote says where the log of a node that does not run ends, under the
// head, when the strip of its problem does not say it already: "worker-1
// is stopped. Its container output ends…"; empty for none.
func (d *pgNodeView) LogsNote() string {
	end := d.LogsEnd()
	if end == "" || d.Diag != nil && d.Diag.ShowAt == 0 {
		return ""
	}
	return d.Node.Short + " is " + string(d.Node.State) + ". " + end
}

// LogsEnded is what the log viewer says once the stream of a node that
// does not run ended, in place of Follow: "Stopped at 12:07:54. The log
// ends there."; empty for a node that runs.
func (d *pgNodeView) LogsEnded() string {
	n := d.Node
	if n.Running() {
		return ""
	}
	if e := n.Exit(); e != nil && !e.At.IsZero() {
		return "Stopped at " + pgHMS(e.At) + ". The log ends there."
	}
	return "The container is " + string(n.State) + ". The log ends there."
}

// --- the menus of the head

// pgMenu is a menu of the verbs of the head of a node page.
type pgMenu struct {
	// ID names it, Label is its button; Icon labels a button that shows a
	// sign only, as ⋯, and Label is then its accessible name.
	ID, Label, Icon string
	Groups          []pgMenuGroup
}

// pgMenuGroup is a part of a menu, with a heading.
type pgMenuGroup struct {
	Label string
	// Danger paints the verbs red, as the ones that can lose data.
	Danger bool
	Items  []pgMenuItem
}

// pgMenuItem is a verb of a menu: a dialog, with a hint of what it does,
// or why it is off now.
type pgMenuItem struct {
	Label, Hint, URL string
	// Off says why the verb does not fit the node now; the item then does
	// nothing and Hint says it.
	Off string
}

// pgNodeMenus returns the menus of the head of a node page: Slurm (Drain,
// Resume) and ⋯ (Delete worker) for a worker, and Power for every node.
// A verb that does not fit the state of the node is off, with why
// (pgOpOff).
func pgNodeMenus(n pgNode) []pgMenu {
	item := func(label, hint, op string) pgMenuItem {
		it := pgMenuItem{Label: label + "…", Hint: hint, URL: n.power("", "", op).URL}
		if canon, _ := action.PowerOpOf(op); pgOpOff(n, canon.ID) != "" {
			it.Label, it.Hint, it.Off = label, pgOpOff(n, canon.ID), pgOpOff(n, canon.ID)
		}
		return it
	}
	power := pgMenu{ID: "power", Label: "Power", Groups: []pgMenuGroup{
		{Label: "Container", Items: []pgMenuItem{
			item("Start", "Start the container.", "start"),
			item("Reboot", "Stop, then start. Jobs on it fail.", "reboot"),
			item("Shut down", "Stop cleanly. Its data stays.", "stop"),
			item("Freeze", "Pause all its processes.", "pause"),
			item("Unfreeze", "Let its processes run on.", "unpause"),
		}},
		{Label: "Can lose data", Danger: true, Items: []pgMenuItem{
			item("Power-cycle", "Kill, then start.", "cycle"),
			item("Cut power", "Kill at once.", "cut"),
		}},
	}}
	if !n.Worker() {
		return []pgMenu{power}
	}
	slurmMenu := pgMenu{ID: "slurm", Label: "Slurm", Groups: []pgMenuGroup{{Label: "scontrol", Items: []pgMenuItem{
		item("Drain", "Start no new job on it. Asks for a reason.", "drain"),
		item("Resume", "Take jobs again.", "resume"),
	}}}}
	del := pgDialogAction("", "", "delete-worker", "realm", n.Realm, "cluster", n.Cluster, "nodes", n.Short)
	more := pgMenu{ID: "more", Label: "More actions", Icon: "⋯", Groups: []pgMenuGroup{{Label: "Worker", Danger: true, Items: []pgMenuItem{
		{Label: "Delete worker…", Hint: "Remove its container. Jobs on it fail.", URL: del.URL},
	}}}}
	return []pgMenu{slurmMenu, power, more}
}

// pgOpOff says why the power op, an id of action.PowerOps, does not fit
// the node now, as "It does not run"; "" when it fits. The menus of a node
// page, the picker of the Power dialog and the selection bar of the Nodes
// tab all ask it: Start fits a node that does not run, Unfreeze a frozen
// one, Drain and Resume a running worker that Slurm lists, the others a
// node that runs; none fits a node that a command creates or deletes.
func pgOpOff(n pgNode, op string) string {
	runs, paused := n.State == cluster.StateRunning, n.State == cluster.StatePaused
	const frozen = "It is frozen: unfreeze it first"
	switch {
	case n.Deleting:
		return "sind deletes it now"
	case n.Creating:
		return "sind creates it now"
	}
	switch op {
	case "on":
		switch {
		case paused:
			return frozen
		case runs:
			return "It runs already"
		}
	case "unfreeze":
		if !paused {
			return "It is not frozen"
		}
	case "drain", "resume":
		switch {
		case !n.Worker():
			return "Only workers take jobs"
		case paused:
			return frozen
		case !runs:
			return "It does not run"
		case n.Known && !n.Managed:
			return "sind runs no Slurm on it"
		case n.Slurm == nil:
			return "sinfo does not list it"
		case op == "drain" && strings.HasPrefix(n.Slurm.State, "drain"):
			return "It is " + strings.TrimSuffix(n.Slurm.State, "*") + " already"
		case op == "resume" && slurm.NodeStateTakesJobs(n.Slurm.State):
			return "It takes jobs already"
		}
	default:
		switch {
		case paused:
			return frozen
		case !runs:
			return "It does not run"
		}
	}
	return ""
}

// pgOpDelete is the op of pgNode.Ops that deletes a worker.
const pgOpDelete = "delete"

// Ops are the ops that fit the node, as the bar of the nodes tab offers
// them: the power ops that pgOpOff lets through, and delete for a worker,
// separated by spaces.
func (n pgNode) Ops() string {
	var ops []string
	for _, op := range action.PowerOps {
		if pgOpOff(n, op.ID) == "" {
			ops = append(ops, op.ID)
		}
	}
	if n.Worker() && !n.Life() {
		ops = append(ops, pgOpDelete)
	}
	return strings.Join(ops, " ")
}

// --- what works on the node, and what changed on it

// pgNodeAct is a command that worked on the node: a run of its event log,
// or a command of sind ui that writes none, such as a restart.
type pgNodeAct struct {
	Title, Link string
	// Source is where it came from (runSource), Class its state.
	Source, Class string
	Start         time.Time
	End           *time.Time
	Running       bool
	// When is when it started, Took how long it ran, once it ended.
	When, Took string
}

// Text says how it stands: "ended ok", "failed", "runs".
func (a pgNodeAct) Text() string {
	switch {
	case a.Running:
		return "runs"
	case a.Class == "ok":
		return "ended ok"
	case a.Class == "warn":
		return "was stopped"
	}
	return "failed"
}

// SourceText says where it came from, for a sentence: "from sind ui".
func (a pgNodeAct) SourceText() string { return runSourceText(a.Source) }

// pgNodeActs returns the commands that worked on the node, the newest
// first: the runs whose command names it, as "power reboot worker-4.dev",
// and the commands of sind ui that name it in their form and write no run,
// such as a restart of a service or a drain.
func pgNodeActs(sn store.Snapshot, n pgNode, now time.Time) []pgNodeAct {
	var out []pgNodeAct
	for _, r := range sn.Runs {
		if !pgNamesNode(r.Title, n) {
			continue
		}
		if t, ok := runTaskOf(sn, r.Trace); ok && t.Realm != "" && t.Realm != n.Realm {
			continue
		}
		pr := pgRunOf(sn, r, now)
		a := pgNodeAct{Title: pr.Title, Link: pr.Link(), Source: pr.Source, Class: pr.Class, Start: r.Start, End: r.End, Running: !r.Done()}
		out = append(out, a)
	}
	for _, t := range sn.Tasks {
		if t.Realm != n.Realm || t.Cluster != n.Cluster || !slices.Contains(action.NodeNames(t.Form), n.Short) {
			continue
		}
		if _, ok := sn.RunOfTrace(t.Trace); ok {
			continue
		}
		a := pgNodeAct{Title: pgTaskTitle(t), Link: "/tasks/" + t.ID, Source: runFromUI, Start: t.Start, End: t.End, Running: !t.Done()}
		switch t.Status {
		case store.TaskRunning:
			a.Class = "run"
		case store.TaskOK:
			a.Class = "ok"
		case store.TaskCanceled:
			a.Class = "warn"
		default:
			a.Class = "bad"
		}
		out = append(out, a)
	}
	for i, a := range out {
		out[i].When = runClock(a.Start, now)
		if a.End != nil {
			out[i].Took = runDur(a.End.Sub(a.Start))
		}
	}
	slices.SortStableFunc(out, func(a, b pgNodeAct) int { return b.Start.Compare(a.Start) })
	return out
}

// pgTaskTitle names a command of sind ui that writes no run: "Restart
// slurmd", "Resume", or its command line.
func pgTaskTitle(t store.Task) string {
	switch t.Action {
	case "restart-service":
		return "Restart " + pgFormValue(t.Form, "unit")
	case "power":
		if op, ok := action.PowerOpOf(pgFormValue(t.Form, "op")); ok {
			return op.Label
		}
	}
	return strings.TrimPrefix(t.Display, "sind ")
}

// pgNamesNode reports whether the command of a run names the node after
// its two command words, as "power reboot worker-[3-4].dev" names
// worker-4.dev.
func pgNamesNode(title string, n pgNode) bool {
	words := strings.Fields(title)
	if len(words) < 3 {
		return false
	}
	for _, w := range words[2:] {
		set, err := nodeset.Parse(w)
		if err != nil {
			continue
		}
		for _, name := range set.Expand() {
			if name == n.Arg || name == n.Short+"."+n.Cluster {
				return true
			}
		}
	}
	return false
}

// pgChangeRow is a row of Recent changes: when, what, and where sind ui saw
// it (systemd, sinfo, docker, sind ui, terminal).
type pgChangeRow struct {
	At   time.Time
	When string
	// Class colours its dot: ok, bad, warn, run.
	Class, Source string
	// Pre, Strong and Post are its text; Strong is a link to Link when it
	// is set.
	Pre, Strong, Post, Link string
}

// pgChangeRows returns what changed on the node, the newest first, at most
// pgChangesShown: what the store saw change (store.Change), what sind ui
// read of a failure that it did not see happen (store.Fault), and the
// commands that worked on it.
func pgChangeRows(sn store.Snapshot, n pgNode, acts []pgNodeAct, now time.Time) []pgChangeRow {
	var out []pgChangeRow
	seen := map[string]bool{}
	for _, c := range sn.Changes(n.Realm, n.Cluster, n.Short) {
		seen[c.Subject] = true
		row := pgChangeRow{At: c.At, Source: c.Source, Class: "bad"}
		if c.OK {
			row.Class = "ok"
		}
		switch c.Source {
		case store.SourceDocker:
			row.Pre, row.Strong = "Container: ", c.State
			if c.State == string(cluster.StatePaused) {
				row.Class = "warn"
			}
		case store.SourceSystemd:
			// The word of its pill, once sind ui read how it stands.
			row.Strong, row.Post = c.Subject, " went down"
			if u, ok := n.Unit(c.Subject); ok && u.State != "" {
				row.Post = " " + u.State
			}
			if c.OK {
				row.Post = " is active"
			}
		default:
			row.Pre, row.Strong = "Slurm: ", c.State
			if !c.OK {
				row.Class = pgSlurmClass(c.State)
			}
			if c.Reason != "" {
				row.Post = ", " + c.Reason
			}
		}
		out = append(out, row)
	}
	if f := n.Fault; f != nil {
		if e := f.Exit; e != nil && !e.At.IsZero() && !seen["container"] {
			out = append(out, pgChangeRow{At: e.At, Source: store.SourceDocker, Class: "bad", Pre: "Container: ", Strong: "stopped", Post: ", " + pgExitShort(*e)})
		}
		for _, u := range f.Units {
			if u.Since.IsZero() || seen[u.Name] {
				continue
			}
			out = append(out, pgChangeRow{At: u.Since, Source: store.SourceSystemd, Class: "bad", Strong: u.Name, Post: " " + pgOr(u.State, "stopped") + pgUnitStatus(u, ", ")})
		}
	}
	for _, a := range acts {
		at := a.Start
		if a.End != nil {
			at = *a.End
		}
		out = append(out, pgChangeRow{At: at, Source: pgSourceWord(a.Source), Class: a.Class, Strong: a.Title, Post: " " + a.Text(), Link: a.Link})
	}
	slices.SortStableFunc(out, func(a, b pgChangeRow) int { return b.At.Compare(a.At) })
	out = out[:min(len(out), pgChangesShown)]
	for i := range out {
		out[i].When = runClock(out[i].At, now)
	}
	return out
}

// pgChangesShown is how many changes Recent changes shows.
const pgChangesShown = 10

// pgSourceWord names where a command came from, in Recent changes.
func pgSourceWord(source string) string {
	if source == runFromUI {
		return "sind ui"
	}
	return runSourceChip(source)
}

// --- the services

// pgSvcRow is a service of the node, with its state, a line on since when
// or how it failed, its journal and, for one that is down, its restart.
type pgSvcRow struct {
	Name string
	Pill pgPill
	Note string
	Logs string
	// Restart is the restart of a service that is down, nil for none.
	Restart *pgAction
}

// pgSvcRows returns the rows of the node's services.
func pgSvcRows(sn store.Snapshot, n pgNode) []pgSvcRow {
	since := map[string]time.Time{}
	for _, c := range slices.Backward(sn.Changes(n.Realm, n.Cluster, n.Short)) {
		if c.Source == store.SourceSystemd {
			since[c.Subject] = c.At
		}
	}
	var out []pgSvcRow
	for _, s := range n.Services {
		row := pgSvcRow{Name: s.Name}
		switch {
		case n.Life() && !s.OK:
			row.Pill = pgPill{n.ServiceWord(), "run"}
		case !n.Running():
			row.Pill = pgPill{"not running", ""}
		case s.OK:
			row.Pill = pgPill{"active", "ok"}
			if at, ok := since[s.Name]; ok {
				row.Note = "since " + pgHMS(at)
			}
		default:
			row.Pill = pgPill{"down", "bad"}
			if u, ok := n.Unit(s.Name); ok {
				row.Pill.Text = pgOr(u.State, "down")
				row.Note = pgUnitStatus(u, "")
				if !u.Since.IsZero() {
					row.Note = strings.TrimSpace(row.Note + " at " + pgHMS(u.Since))
					if u.Status == 0 {
						row.Note = "since " + pgHMS(u.Since)
					}
				}
			}
			if !n.Life() && !n.Busy && n.CanRestart() {
				a := n.RestartAction(s.Name, "sm")
				a.Label = "Restart…"
				row.Restart = &a
			}
		}
		if n.Running() {
			row.Logs = pgNodeLogsURL(n.Realm, n.Cluster, n.Short, s.Name)
		}
		out = append(out, row)
	}
	return out
}

// --- the card of the problem

// pgDiag is the card of a node that needs a look: what is wrong and what
// it means for the jobs, the facts sind ui read, the end of the journal of
// the failed unit, and the fixes to try in order.
type pgDiag struct {
	// Sev is bad or warn.
	Sev   string
	Title string
	// Since says since when, as "since 09:02 · 29 min".
	Since string
	// Text says what the service does, what happened and what Slurm does
	// now; Pill is the Slurm state that it names, empty for none.
	Text string
	// Facts are the facts that sind ui read, as "failed: 09:02:17, exit
	// status 1"; Last is the last command on the node.
	Facts []pgFact
	Last  *pgNodeAct
	// Journal is the end of the journal of the failed unit, nil for none.
	Journal *pgJournal
	// Ladder are the fixes, the one to try first first.
	Ladder []pgRung
	// Again says that a command fixed the problem a moment ago, but that
	// it came back, or is still there.
	Again string
	// ShowAt is when the page shows the card, in milliseconds since 1970,
	// once the card of a fix that waits hides; 0 to show it at once.
	ShowAt int64
}

// pgFact is a fact of the card, as "failed" and "09:02:17, exit status 1".
// Rep is set on a fact that the text of the card says too, which a phone
// leaves out.
type pgFact struct {
	Label, Text string
	Rep         bool
}

// pgJournal is the end of the journal of a unit, as the card shows it.
type pgJournal struct {
	Unit  string
	Lines []pgJournalLine
	// Link opens the Logs tab with the unit's journal; Text is the lines
	// for Copy.
	Link, Text string
}

// pgJournalLine is a line of the journal, with the class of its level.
type pgJournalLine struct{ When, Text, Class string }

// pgDiagOf builds the card of the node's problem, nil when it needs no look
// or a command works on it.
func pgDiagOf(sn store.Snapshot, n pgNode, acts []pgNodeAct, now time.Time) *pgDiag {
	if !n.Problem() || n.Busy {
		return nil
	}
	d := &pgDiag{Sev: pgOr(n.Sev(), "warn"), Ladder: pgLadder(n)}
	if i := slices.IndexFunc(acts, func(a pgNodeAct) bool { return !a.Running }); i >= 0 {
		d.Last = &acts[i]
	}
	jobs, its := "", ""
	if n.schedules() {
		jobs, its = ", so "+n.Short+" takes no jobs", ", so it takes no jobs"
	}
	switch {
	case n.State == cluster.StatePaused:
		d.Title = n.Short + " is frozen" + its
		d.Text = "The container is paused (docker pause): its processes do not run, and Slurm sees the node stop answering. Unfreeze lets them run on where they stopped."
		d.Since = pgSinceChange(sn, n, store.SourceDocker, now)
	case n.State == cluster.StateStopped:
		pgDiagStopped(d, n, its, now)
	case n.State != cluster.StateRunning:
		d.Title = "Docker reports a state of " + n.Short + " that sind does not know"
		d.Text = "Its state is " + string(n.State) + ". Its logs may say more."
	case len(n.Down()) > 0:
		pgDiagService(d, n, jobs, now)
	default:
		pgDiagSlurm(sn, d, n, now)
	}
	if !n.schedules() {
		d.Text += " " + pgNodeEffect(n, pgSnapNodes(sn, n.Realm, now))
	}
	return d
}

// pgDiagService fills the card of a running node with a service down.
func pgDiagService(d *pgDiag, n pgNode, jobs string, now time.Time) {
	down, unit := n.Down(), n.FailedUnit()
	verb := "failed"
	u, read := n.Unit(unit)
	if read && u.State != "" && u.State != "failed" {
		verb = "is " + u.State
	}
	d.Title = unit + " " + verb + jobs
	if others := slices.DeleteFunc(slices.Clone(down), func(s string) bool { return s == unit }); len(others) > 0 {
		d.Title = pgJoin(append([]string{unit}, others...)) + " are down" + jobs
	}
	text := []string{unit + " is " + pgUnitRole(unit) + "."}
	if read {
		how := "It stopped" + pgUnitStatus(u, " with ")
		if !u.Since.IsZero() {
			how += " at " + pgHMS(u.Since)
			d.Since = "since " + pgHM(u.Since) + " · " + pgAge(u.Since, now)
			d.Facts = append(d.Facts, pgFact{"failed", pgHMS(u.Since) + pgUnitStatus(u, ", "), true})
		}
		text = append(text, how+".")
		if len(u.Journal) > 0 {
			j := &pgJournal{Unit: unit, Link: pgNodeLogsURL(n.Realm, n.Cluster, n.Short, unit)}
			var lines []string
			for _, l := range u.Journal {
				j.Lines = append(j.Lines, pgJournalLine{When: pgHMS(l.At), Text: l.Text, Class: pgLineClass(l.Text)})
				lines = append(lines, pgHMS(l.At)+" "+l.Text)
			}
			j.Text = strings.Join(lines, "\n")
			d.Journal = j
		}
	}
	if n.Slurm != nil && !slurm.NodeStateTakesJobs(n.Slurm.State) {
		s := "Slurm now marks " + n.Short + " " + n.Slurm.State
		if n.Slurm.Reason != "" {
			s += " (" + n.Slurm.Reason + ")"
		}
		text = append(text, s+", and no job starts on it.")
	} else if n.schedules() && unit == "slurmd" {
		text = append(text, "No job starts on it.")
	}
	var up []string
	for _, s := range n.Services {
		if s.OK {
			up = append(up, s.Name)
		}
	}
	if len(up) > 0 {
		verb := "is"
		if len(up) > 1 {
			verb = "are"
		}
		text = append(text, "The container runs; "+pgJoin(up)+" "+verb+" active.")
	}
	d.Text = strings.Join(text, " ")
}

// pgDiagStopped fills the card of a stopped node.
func pgDiagStopped(d *pgDiag, n pgNode, jobs string, now time.Time) {
	d.Title = n.Short + " is stopped" + jobs
	d.Text = "The container does not run."
	if e := n.Exit(); e != nil {
		d.Text = "The container stopped"
		if !e.At.IsZero() {
			d.Text += " at " + pgHMS(e.At)
			d.Since = "since " + pgHM(e.At) + " · " + pgAge(e.At, now)
		}
		d.Text += fmt.Sprintf(" with exit code %d: %s.", e.Code, pgExitText(*e))
		fact := pgExitShort(*e)
		if !e.At.IsZero() {
			fact = pgHMS(e.At) + ", " + fact
		}
		d.Facts = append(d.Facts, pgFact{"stopped", fact, true})
		if e.Error != "" {
			d.Facts = append(d.Facts, pgFact{"Docker said", e.Error, false})
		}
	}
	d.Text += " Start brings it back; systemd then starts its services."
}

// pgDiagSlurm fills the card of a worker that runs with its services up,
// but that Slurm sends no jobs.
func pgDiagSlurm(sn store.Snapshot, d *pgDiag, n pgNode, now time.Time) {
	state := n.Slurm.State
	reason := n.Slurm.Reason
	switch {
	case strings.HasPrefix(state, "draining"):
		d.Title = n.Short + " is draining: it ends its jobs and takes no new ones"
	case strings.HasPrefix(state, "drain"):
		d.Title = n.Short + " is drained: Slurm starts no new job on it"
	default:
		d.Title = "Slurm marks " + n.Short + " " + state + ": it takes no jobs"
	}
	text := ""
	if reason != "" {
		text = "Reason: " + reason + ". "
	}
	text += "Its services are active and its container runs."
	if strings.HasPrefix(state, "drain") {
		text += " A drain is often on purpose: resume it when the cause is gone."
	} else {
		text += " slurmctld does not count it as up. Resume asks Slurm to take it back."
	}
	d.Text = text
	d.Since = pgSinceChange(sn, n, store.SourceSlurm, now)
}

// pgSinceChange says since when the node is as it is, by the newest change
// of the source that sind ui saw: "since 09:02 · 29 min"; with none, it was
// so when sind ui first read it.
func pgSinceChange(sn store.Snapshot, n pgNode, source string, now time.Time) string {
	changes := sn.Changes(n.Realm, n.Cluster, n.Short)
	if i := slices.IndexFunc(changes, func(c store.Change) bool { return c.Source == source }); i >= 0 {
		return "since " + pgHM(changes[i].At) + " · " + pgAge(changes[i].At, now)
	}
	return "since before sind ui started"
}

// pgAge says how long ago t was, short: "29 min", "3 h", "just now".
func pgAge(t, now time.Time) string { return strings.TrimSuffix(pgAgo(t, now), " ago") }

// pgLineClass is the class of a line of a journal: an error, a warning, or
// plain.
func pgLineClass(text string) string {
	l := strings.ToLower(text)
	switch {
	case strings.Contains(l, "error") || strings.Contains(l, "fatal") || strings.Contains(l, "fail"):
		return "err"
	case strings.Contains(l, "warn"):
		return "warn"
	}
	return ""
}

// --- the card of a fix

// pgFixed is the card that says that a command of sind ui fixed the node
// a moment ago: what ran, how long, and what sind ui checked again. Until
// the node's status shows the fix hold, as when a service still starts,
// the card waits (Wait) and says what it waits for.
type pgFixed struct {
	Title, What string
	Wait        bool
	Checks      []pgFixCheck
	// Cmd is the command that ran, Link its page.
	Cmd, Link string
	// HideAt is when the page hides the card, in milliseconds since 1970,
	// and Left how many seconds that is from now.
	HideAt int64
	Left   int
	// Quiet is when the node last changed, in milliseconds since 1970, and
	// QuietFor how long ago that is; Unit is what may fail again.
	Quiet    int64
	QuietFor string
	Unit     string
	// back is set when sind ui saw the problem come back (pgCameBack);
	// waits is the title of the card while it waits.
	back  bool
	waits string
}

// pgFixCheck is a check of the card of a fix, and whether it holds.
type pgFixCheck struct {
	Text string
	OK   bool
}

// Class is the class of the card: ok, or run while it waits.
func (f *pgFixed) Class() string {
	if f.Wait {
		return "run"
	}
	return "ok"
}

// pgFixedOf returns the card of a fix of the node, nil for none: a command
// of sind ui that may fix it (store.Task.Fixes) ended ok within fixedFor.
// Each check says what the node's status says now. The caller decides what
// shows when the node still needs a look (pgLoadNode).
func pgFixedOf(sn store.Snapshot, n pgNode, now time.Time) *pgFixed {
	var t *store.Task
	for i := range slices.Backward(sn.Tasks) {
		c := sn.Tasks[i]
		if c.Status == store.TaskOK && c.End != nil && now.Sub(*c.End) < fixedFor && c.Realm == n.Realm && c.Cluster == n.Cluster && slices.Contains(c.Fixes, n.Short) {
			t = &c
			break
		}
	}
	if t == nil {
		return nil
	}
	changes := sn.Changes(n.Realm, n.Cluster, n.Short)
	f := &pgFixed{
		Cmd: t.Display, Link: "/tasks/" + t.ID, HideAt: t.End.Add(fixedFor).UnixMilli(),
		Left: int((fixedFor - now.Sub(*t.End) + time.Second - 1) / time.Second), back: pgCameBack(changes, *t),
	}
	if r, ok := sn.RunOfTrace(t.Trace); ok {
		f.Link = pgRun{ID: r.ID}.Link()
	}
	name := pgTaskTitle(*t)
	f.What = fmt.Sprintf("%s ended ok at %s, in %s.", name, pgHMS(*t.End), runDur(t.End.Sub(t.Start)))
	unit, op := pgFormValue(t.Form, "unit"), pgFormValue(t.Form, "op")
	check := func(ok bool, yes, no string) {
		text := no
		if ok {
			text = yes
		}
		f.Checks = append(f.Checks, pgFixCheck{text, ok})
	}
	if t.Action != "restart-service" && op != "resume" {
		check(n.Running(), "The container runs.", "The container does not run yet.")
	}
	since := map[string]time.Time{}
	last := *t.End
	for _, c := range changes {
		if _, ok := since[c.Subject]; !ok {
			since[c.Subject] = c.At
		}
		if c.At.After(last) {
			last = c.At
		}
	}
	for _, s := range n.Services {
		if unit != "" && s.Name != unit {
			continue
		}
		yes := s.Name + " is active"
		if at, ok := since[s.Name]; ok {
			yes += " since " + pgHMS(at)
		}
		check(s.OK && n.Running(), yes+".", s.Name+" is not active yet.")
	}
	takes := false
	if n.Slurm != nil {
		takes = slurm.NodeStateTakesJobs(n.Slurm.State)
		check(takes, "Slurm state is "+n.Slurm.State+": new jobs can start on "+n.Short+".",
			"Slurm state is "+n.Slurm.State+": no new job starts on "+n.Short+" yet.")
	}
	f.Wait = slices.ContainsFunc(f.Checks, func(c pgFixCheck) bool { return !c.OK })
	f.waits = name + " ended ok: sind ui checks " + n.Short + " again"
	jobs := takes && n.schedules()
	switch {
	case f.Wait:
		f.Title = f.waits
	case t.Action == "restart-service":
		f.Title = "Fixed: " + unit + " is active again"
		if jobs {
			f.Title += ", and " + n.Short + " takes jobs"
		}
	case n.Slurm != nil && op == "resume":
		f.Title = "Fixed: " + n.Short + " takes jobs again"
	default:
		f.Title = "Fixed: " + n.Short + " runs again"
		if jobs {
			f.Title += " and takes jobs"
		}
	}
	f.Unit = pgOr(unit, n.Short)
	f.Quiet, f.QuietFor = last.UnixMilli(), runDur(max(now.Sub(last), 0))
	return f
}

// pgCameBack reports whether sind ui saw the problem that the task fixed
// come back: what it fixed turned good after the task started, and bad
// again after it ended. What it fixed is the unit of a restart, the Slurm
// state of a resume, and else the container and its services. A problem
// that stays, or a change that sind ui has not seen yet, is no come-back.
func pgCameBack(changes []store.Change, t store.Task) bool {
	unit, op := pgFormValue(t.Form, "unit"), pgFormValue(t.Form, "op")
	fixed := func(c store.Change) bool {
		switch {
		case t.Action == "restart-service":
			return c.Source == store.SourceSystemd && c.Subject == unit
		case op == "resume":
			return c.Source == store.SourceSlurm
		}
		return c.Source != store.SourceSlurm
	}
	good := map[string]bool{}
	for _, c := range slices.Backward(changes) {
		if c.At.Before(t.Start) || !fixed(c) {
			continue
		}
		if c.OK {
			good[c.Subject] = true
		} else if good[c.Subject] && !c.At.Before(*t.End) {
			return true
		}
	}
	return false
}
