// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/GSI-HPC/go-clikit/doctor"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
)

func init() { defaultRegistry.AddRoute(pgRealmRoute(overviewPage, false, false)) }

// overviewPage is the Overview page, template overview.tmpl, served by
// pgRealmRoute. It spans every realm; ?realm= narrows it to one, which the
// realms panel and the realm picker link to, and which the realm cookie
// remembers. Without ?realm= it shows every realm, whatever the cookie
// says: the page is the Overview group of the rail, not the Realm group.
var overviewPage = Page{
	Name:      "overview",
	Pattern:   "GET /{$}",
	Title:     "Overview",
	Nav:       "overview",
	Params:    []string{"realm"},
	Fragments: []string{"pg-overview-head", "pg-overview-cards", "pg-overview-needs", "pg-overview-recent"},
	Load:      func(v *View) (err error) { v.Data, err = pgLoadOverview(v); return },
}

// recentRuns is how many runs the Overview lists.
const recentRuns = 6

// failedWindow is how long a failed run stays in "Needs you".
const failedWindow = 6 * time.Hour

// pgOverviewTopics are what every part of the Overview depends on.
var pgOverviewTopics = []string{"realms", "clusters/", "nodes/", "status/", "lock/", "monitoring/", "docker", "doctor", "run"}

// pgOverviewFragments returns the fragments of the Overview; the last, the
// recent runs, depends on the runs only.
func pgOverviewFragments() []Fragment {
	out := pgPageFragments(overviewPage, pgOverviewTopics)
	out[len(out)-1].Prefixes = []string{"run"}
	return out
}

// pgRealmRow is a realm in the realms panel of the Overview.
type pgRealmRow struct {
	Name     string
	Clusters int
	Nodes    int
	// Look counts the nodes that need a look (pgNode.Problem), LookClass
	// is the class of that count (pgLook).
	Look      int
	LookClass string
	// Lives say which clusters of the realm a command creates or deletes
	// now, as "creating nightly".
	Lives  []pgPill
	Locked bool
	On     bool
	// Delete opens the Delete realm dialog.
	Delete pgAction
}

// pgNeed is an item of "Needs you".
type pgNeed struct {
	// Sev is bad, run or warn.
	Sev string
	// Kind is what the item is about, one of the pgNeed constants; the
	// head of the Overview counts the items by it.
	Kind    string
	Title   string
	Detail  string
	Actions []pgAction
}

// The kinds of the items of "Needs you".
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
)

// pgNeedWords say how many items of a kind there are, in the order of the
// head of the Overview: the kinds, then the words for one and for more.
// Every item of a node counts as one node that needs a look, as the rail
// and the realms count them. A lock is work that goes on, not a problem,
// so it has none.
var pgNeedWords = []struct {
	Kinds     []string
	One, Many string
}{
	{[]string{pgNeedDocker}, "Docker is not reachable", "Docker is not reachable"},
	{[]string{pgNeedStopped, pgNeedPaused, pgNeedUnknown, pgNeedService, pgNeedSlurm}, "node needs a look", "nodes need a look"},
	{[]string{pgNeedRun}, "run failed", "runs failed"},
	{[]string{pgNeedFailed}, "host check failed", "host checks failed"},
	{[]string{pgNeedWarning}, "host warning", "host warnings"},
}

// pgOverview is what the Overview shows.
type pgOverview struct {
	// Realm is the realm the page is narrowed to, "" for all.
	Realm    string
	Realms   []pgRealmRow
	Clusters []pgCluster
	Nodes    int
	Needs    []pgNeed
	Runs     []pgRun
	Actions  []pgAction
	// Empty is set when no realm, cluster or node is known.
	Empty bool
}

// pgLoadOverview builds the Overview from the view's snapshot.
func pgLoadOverview(v *View) (*pgOverview, error) {
	realm, err := pgRealmParam(v)
	if err != nil {
		return nil, err
	}
	sn := v.Snap
	d := &pgOverview{Realm: realm}
	d.Realms = pgRealmRows(sn, realm, v.Now)
	d.Empty = len(d.Realms) == 0
	for _, c := range pgClustersOf(sn, realm, v.Now) {
		d.Clusters = append(d.Clusters, c)
		d.Nodes += len(c.Nodes)
	}
	d.Needs = pgNeeds(sn, realm, v.Now)
	for _, r := range sn.Runs[:min(recentRuns, len(sn.Runs))] {
		d.Runs = append(d.Runs, pgRunOf(sn, r, v.Now))
	}
	d.Actions = pgQuickActions(d)
	return d, nil
}

// pgQuickActions returns the buttons at the top of the Overview: the
// dialogs that start most work. A realm is passed on when it is clear which
// one is meant.
func pgQuickActions(d *pgOverview) []pgAction {
	realm := d.Realm
	if realm == "" && len(d.Realms) == 1 {
		realm = d.Realms[0].Name
	}
	acts := []pgAction{pgDialogAction("New cluster…", "primary", "create-cluster", "realm", realm)}
	if len(d.Clusters) > 0 {
		c := d.Clusters[0]
		acts = append(acts, pgDialogAction("Add workers…", "", "create-worker", "realm", c.Realm, "cluster", c.Name))
	}
	return append(acts, pgDialogAction("Connect a container…", "", "connect", "realm", realm))
}

// pgSevOrder ranks the severities of "Needs you": bad first.
var pgSevOrder = []string{"bad", "run", "warn"}

// pgNeeds returns what needs the user, in the realm or in all realms: a
// Docker that does not answer, locks that are held, nodes that are not
// running, runs that failed lately and host checks that did not pass. The
// worst come first.
func pgNeeds(sn store.Snapshot, realm string, now time.Time) []pgNeed {
	var out []pgNeed
	if sn.Docker.Checked && sn.Docker.Err != "" {
		out = append(out, pgNeed{
			Sev: "bad", Kind: pgNeedDocker, Title: "Docker is not reachable", Detail: sn.Docker.Err + ". The pages show what sind ui read last.",
			Actions: []pgAction{{Label: "Doctor", URL: "/doctor", Class: "sm ghost"}},
		})
	}
	for _, r := range pgRealmNames(sn, realm) {
		if h := sn.Locks[r]; h != nil {
			out = append(out, pgNeed{
				Sev: "run", Kind: pgNeedLock, Title: "A command holds the lock of realm " + r,
				Detail:  h.String() + ". Actions on this realm wait for it.",
				Actions: []pgAction{{Label: "Show activity", URL: "/activity", Class: "sm ghost"}},
			})
		}
		for _, n := range pgSnapNodes(sn, r, now) {
			if n.Problem() {
				out = append(out, pgNodeNeed(sn, n))
			}
		}
	}
	for _, r := range sn.Runs {
		if r.Done() && r.Status == store.StatusFailed && now.Sub(*r.End) < failedWindow {
			out = append(out, pgNeed{
				Sev: "bad", Kind: pgNeedRun, Title: fmt.Sprintf("%s failed %s", r.Title, pgAgo(*r.End, now)), Detail: r.Err,
				Actions: []pgAction{{Label: "Show what failed", URL: pgRun{ID: r.ID}.Link(), Class: "sm ghost"}},
			})
		}
	}
	if sn.Doctor != nil {
		for _, c := range sn.Doctor.Checks {
			if c.Status == doctor.StatusOK {
				continue
			}
			sev, kind := "warn", pgNeedWarning
			if c.Status == doctor.StatusFailed {
				sev, kind = "bad", pgNeedFailed
			}
			out = append(out, pgNeed{
				Sev: sev, Kind: kind, Title: "Doctor: " + c.Name, Detail: c.Detail,
				Actions: []pgAction{{Label: "Show fix", URL: "/doctor", Class: "sm ghost"}},
			})
		}
	}
	slices.SortStableFunc(out, func(a, b pgNeed) int {
		return slices.Index(pgSevOrder, a.Sev) - slices.Index(pgSevOrder, b.Sev)
	})
	return out
}

// pgNodeNeed describes a node that needs a look: one that does not run, or
// runs with a service down.
func pgNodeNeed(sn store.Snapshot, n pgNode) pgNeed {
	need := pgNeed{Sev: "warn", Kind: pgNeedUnknown, Title: fmt.Sprintf("%s is %s", n.Arg, n.State)}
	cs := "unknown"
	if c, ok := sn.Cluster(n.Realm, n.Cluster); ok {
		cs = string(c.State)
	}
	logs := pgAction{Label: "Logs", URL: n.LogsLink(), Class: "sm ghost"}
	switch {
	case n.State == cluster.StateRunning && len(n.Down()) == 0:
		// Slurm sends the node no jobs (pgNode.SlurmIssue).
		need.Sev, need.Kind = "warn", pgNeedSlurm
		need.Title = fmt.Sprintf("%s is %s in Slurm", n.Arg, n.Slurm.State)
		need.Detail = "Slurm sends it no new jobs."
		if n.Slurm.Reason != "" {
			need.Detail += " Reason: " + n.Slurm.Reason + "."
		}
		need.Actions = []pgAction{logs, n.power("Resume…", "sm", "resume")}
	case n.State == cluster.StateRunning:
		down := n.Down()
		need.Sev, need.Kind = "bad", pgNeedService
		need.Title = fmt.Sprintf("%s has %s down", n.Arg, strings.Join(down, ", "))
		verb := "is"
		if len(down) > 1 {
			verb = "are"
		}
		need.Detail = fmt.Sprintf("The container runs, but %s %s not active.", pgJoin(down), verb)
		need.Actions = []pgAction{logs, n.power("Reboot…", "sm", "reboot")}
	case n.State == cluster.StateStopped:
		need.Sev, need.Kind = "bad", pgNeedStopped
		need.Detail = fmt.Sprintf("The container is not running. The cluster %s is %s.", n.Cluster, cs)
		need.Actions = []pgAction{n.power("Start…", "sm", "start"), logs}
	case n.State == cluster.StatePaused:
		need.Kind = pgNeedPaused
		need.Detail = fmt.Sprintf("The container is paused. The cluster %s is %s.", n.Cluster, cs)
		need.Actions = []pgAction{n.power("Unfreeze…", "sm", "unpause"), logs}
	default:
		need.Detail = fmt.Sprintf("Docker reports a state that sind does not know. The cluster %s is %s.", n.Cluster, cs)
		need.Actions = []pgAction{logs}
	}
	return need
}

// RealmsText returns "2 realms".
func (d *pgOverview) RealmsText() string { return pgPlural(len(d.Realms), "realm", "realms") }

// ClustersText returns "3 clusters".
func (d *pgOverview) ClustersText() string { return pgPlural(len(d.Clusters), "cluster", "clusters") }

// NodesText returns "12 nodes".
func (d *pgOverview) NodesText() string { return pgPlural(d.Nodes, "node", "nodes") }

// NeedsText returns "3 items".
func (d *pgOverview) NeedsText() string { return pgPlural(len(d.Needs), "item", "items") }

// Summary names the problems of "Needs you" for the head of the Overview,
// as "2 nodes need a look · 1 run failed"; empty when there is none.
func (d *pgOverview) Summary() string {
	count := map[string]int{}
	for _, n := range d.Needs {
		count[n.Kind]++
	}
	var parts []string
	for _, w := range pgNeedWords {
		c := 0
		for _, k := range w.Kinds {
			c += count[k]
		}
		switch {
		case c == 0:
		case w.Kinds[0] == pgNeedDocker:
			parts = append(parts, w.One)
		default:
			parts = append(parts, pgPlural(c, w.One, w.Many))
		}
	}
	return strings.Join(parts, " · ")
}

// SummaryClass returns the class of the pill of Summary: bad when an item
// is bad, else warn.
func (d *pgOverview) SummaryClass() string {
	if slices.ContainsFunc(d.Needs, func(n pgNeed) bool { return n.Sev == "bad" }) {
		return "bad"
	}
	return "warn"
}

// LookText returns "2 nodes need a look".
func (r pgRealmRow) LookText() string { return pgLookText(r.Look) }
