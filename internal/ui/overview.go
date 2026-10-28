// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"slices"
	"strings"
	"time"
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
var pgOverviewTopics = []string{"realms", "clusters/", "nodes/", "status/", "lock/", "monitoring/", "docker", "doctor", "run", "tasks", "needs"}

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
	// Menu is the ⋯ menu of the row: Connect a container, and Delete
	// realm apart.
	Menu pgMenu
}

// pgNeedWords say how many items of a kind there are, in the order of the
// head of the Overview: the kinds, then the words for one and for more.
// Every item of a node counts as one node that needs a look, as the rail
// and the realms count them. A row that is fixed has none.
var pgNeedWords = []struct {
	Kinds     []string
	One, Many string
}{
	{[]string{pgNeedDocker}, "Docker is not reachable", "Docker is not reachable"},
	{[]string{pgNeedStopped, pgNeedPaused, pgNeedUnknown, pgNeedService, pgNeedSlurm}, "node needs a look", "nodes need a look"},
	{[]string{pgNeedRun}, "run failed", "runs failed"},
	{[]string{pgNeedFailed}, "host check failed", "host checks failed"},
	{[]string{pgNeedWarning}, "host warning", "host warnings"},
	{[]string{pgNeedLock}, "realm stays locked", "realms stay locked"},
}

// pgOverview is what the Overview shows.
type pgOverview struct {
	// Realm is the realm the page is narrowed to, "" for all.
	Realm    string
	Realms   []pgRealmRow
	Clusters []pgCluster
	Nodes    int
	Needs    []pgNeed
	// Running are the commands that run now, in the strip "Running now".
	Running []pgNowRun
	Runs    []pgRun
	Actions []pgAction
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
	pgNeedsOf(d, sn, v.Now)
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
	// Add workers names the cluster only when there is one: else the
	// dialog asks for it, so that no cluster is picked for the user.
	switch len(d.Clusters) {
	case 0:
	case 1:
		c := d.Clusters[0]
		acts = append(acts, pgDialogAction("Add workers…", "", "create-worker", "realm", c.Realm, "cluster", c.Name))
	default:
		acts = append(acts, pgDialogAction("Add workers…", "", "create-worker", "realm", realm))
	}
	return append(acts, pgDialogAction("Connect a container…", "", "connect", "realm", realm))
}

// RealmsText returns "2 realms".
func (d *pgOverview) RealmsText() string { return pgPlural(len(d.Realms), "realm", "realms") }

// ClustersText returns "3 clusters".
func (d *pgOverview) ClustersText() string { return pgPlural(len(d.Clusters), "cluster", "clusters") }

// NodesText returns "12 nodes".
func (d *pgOverview) NodesText() string { return pgPlural(d.Nodes, "node", "nodes") }

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
