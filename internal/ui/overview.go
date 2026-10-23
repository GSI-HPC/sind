// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"slices"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/doctor"
)

func init() { RegisterRoute(pgRealmRoute(overviewPage, false)) }

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
	Load: func(v *View) error {
		_, err := pgLoadOverview(v)
		return err
	},
}

// recentRuns is how many runs the Overview lists.
const recentRuns = 6

// failedWindow is how long a failed run stays in "Needs you".
const failedWindow = 6 * time.Hour

// pgOverviewTopics are what every part of the Overview depends on.
var pgOverviewTopics = []string{"realms", "clusters/", "nodes/", "lock/", "monitoring/", "docker", "doctor", "run"}

// pgOverviewFragments returns the fragments of the Overview.
func pgOverviewFragments() []Fragment {
	load := func(v *View) error {
		d, err := pgLoadOverview(v)
		v.Data = d
		return err
	}
	return []Fragment{
		{Name: "pg-overview-head", Params: []string{"realm"}, Prefixes: pgOverviewTopics, Load: load},
		{Name: "pg-overview-cards", Params: []string{"realm"}, Prefixes: pgOverviewTopics, Load: load},
		{Name: "pg-overview-needs", Params: []string{"realm"}, Prefixes: pgOverviewTopics, Load: load},
		{Name: "pg-overview-recent", Params: []string{"realm"}, Prefixes: []string{"run"}, Load: load},
	}
}

// pgRealmRow is a realm in the realms panel of the Overview.
type pgRealmRow struct {
	Name     string
	Clusters int
	Nodes    int
	Problems int
	Locked   bool
	On       bool
}

// pgNeed is an item of "Needs you".
type pgNeed struct {
	// Sev is bad, run or warn.
	Sev     string
	Title   string
	Detail  string
	Actions []pgAction
}

// pgOverview is what the Overview shows.
type pgOverview struct {
	// Realm is the realm the page is narrowed to, "" for all.
	Realm    string
	Realms   []pgRealmRow
	Clusters []pgCluster
	Nodes    int
	Problems int
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
	for _, r := range pgRealmNames(sn, "") {
		row := pgRealmRow{Name: r, Clusters: len(sn.ClustersOf(r)), Nodes: len(sn.NodesOf(r)), Locked: sn.Lock(r) != nil, On: r == realm}
		for _, n := range sn.NodesOf(r) {
			if n.State != cluster.StateRunning {
				row.Problems++
			}
		}
		d.Realms = append(d.Realms, row)
	}
	d.Empty = len(d.Realms) == 0
	for _, c := range pgClustersOf(sn, realm, v.Now) {
		d.Clusters = append(d.Clusters, c)
		d.Nodes += len(c.Nodes)
		for _, n := range c.Nodes {
			if n.Problem() {
				d.Problems++
			}
		}
	}
	d.Needs = pgNeeds(sn, realm, v.Now)
	for _, r := range sn.Runs[:min(recentRuns, len(sn.Runs))] {
		d.Runs = append(d.Runs, pgRunOf(r, v.Now))
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
			Sev: "bad", Title: "Docker is not reachable", Detail: sn.Docker.Err + ". The pages show what sind ui read last.",
			Actions: []pgAction{{Label: "Doctor", URL: "/doctor", Class: "sm ghost"}},
		})
	}
	for _, r := range pgRealmNames(sn, realm) {
		if h := sn.Lock(r); h != nil {
			out = append(out, pgNeed{
				Sev: "run", Title: "A command holds the lock of realm " + r,
				Detail:  h.String() + ". Actions on this realm wait for it.",
				Actions: []pgAction{{Label: "Show activity", URL: "/activity", Class: "sm ghost"}},
			})
		}
		for _, ns := range sn.NodesOf(r) {
			if ns.State != cluster.StateRunning {
				out = append(out, pgNodeNeed(sn, pgNodeOf(r, ns, nil)))
			}
		}
	}
	for _, r := range sn.Runs {
		if r.Done() && r.Status == store.StatusFailed && now.Sub(*r.End) < failedWindow {
			out = append(out, pgNeed{
				Sev: "bad", Title: fmt.Sprintf("%s failed %s", r.Title, pgAgo(*r.End, now)), Detail: r.Err,
				Actions: []pgAction{{Label: "Show what failed", URL: pgRun{ID: r.ID}.Link(), Class: "sm ghost"}},
			})
		}
	}
	if sn.Doctor != nil {
		for _, c := range sn.Doctor.Checks {
			if c.Status == doctor.StatusOK {
				continue
			}
			sev := "warn"
			if c.Status == doctor.StatusFailed {
				sev = "bad"
			}
			out = append(out, pgNeed{
				Sev: sev, Title: "Doctor: " + c.Name, Detail: c.Detail,
				Actions: []pgAction{{Label: "Show fix", URL: "/doctor", Class: "sm ghost"}},
			})
		}
	}
	slices.SortStableFunc(out, func(a, b pgNeed) int {
		return slices.Index(pgSevOrder, a.Sev) - slices.Index(pgSevOrder, b.Sev)
	})
	return out
}

// pgNodeNeed describes a node that is not running.
func pgNodeNeed(sn store.Snapshot, n pgNode) pgNeed {
	need := pgNeed{Sev: "warn", Title: fmt.Sprintf("%s is %s", n.Arg, n.State)}
	cs := "unknown"
	if c, ok := sn.Cluster(n.Realm, n.Cluster); ok {
		cs = string(c.State)
	}
	logs := pgAction{Label: "Logs", URL: pgClusterURL(n.Realm, n.Cluster, "logs"), Class: "sm ghost"}
	switch n.State {
	case cluster.StateStopped:
		need.Sev = "bad"
		need.Detail = fmt.Sprintf("The container is not running. The cluster %s is %s.", n.Cluster, cs)
		need.Actions = []pgAction{pgDialogAction("Start…", "sm", "power", "realm", n.Realm, "cluster", n.Cluster, "nodes", n.Short, "op", "start"), logs}
	case cluster.StatePaused:
		need.Detail = fmt.Sprintf("The container is paused. The cluster %s is %s.", n.Cluster, cs)
		need.Actions = []pgAction{pgDialogAction("Unfreeze…", "sm", "power", "realm", n.Realm, "cluster", n.Cluster, "nodes", n.Short, "op", "unpause"), logs}
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

// ProblemsText returns "2 nodes need a look".
func (d *pgOverview) ProblemsText() string {
	return pgPlural(d.Problems, "node needs", "nodes need") + " a look"
}

// NeedsText returns "3 items".
func (d *pgOverview) NeedsText() string { return pgPlural(len(d.Needs), "item", "items") }

// ProblemsText returns "2 nodes are not running".
func (r pgRealmRow) ProblemsText() string {
	return pgPlural(r.Problems, "node is", "nodes are") + " not running"
}
