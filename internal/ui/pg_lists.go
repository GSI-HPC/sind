// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"github.com/GSI-HPC/sind/internal/ui/store"
)

func init() { pgRegister(defaultRegistry) }

// pgRegister adds the main pages of sind ui to r: their pages, fragments,
// routes, and the counts of the rail.
func pgRegister(r *Registry) {
	for _, f := range pgOverviewFragments() {
		r.AddFragment(f)
	}
	r.AddFragment(pgClustersFragment())
	r.AddFragment(pgNodesFragment())
	for _, f := range pgClusterFragments() {
		r.AddFragment(f)
	}
	for _, f := range pgNodeFragments() {
		r.AddFragment(f)
	}
	// The list pages read the realm cookie, which a Page cannot see, so
	// they are routes; the Overview and the pages of one cluster or node
	// are pages.
	r.AddRoute(pgListRoute(pgClustersPage))
	r.AddRoute(pgListRoute(pgNodesPage))
	r.AddRoute(pgRealmRoute())
	r.AddPage(pgClusterPage)
	r.AddRoute(pgClusterByNameRoute())
	r.AddPage(pgNodePage)
	pgNavCounts(r)
}

// pgClustersPage is the Clusters page. It is served by pgListRoute, which
// builds its handler from this value, so the registry holds no page of it.
var pgClustersPage = Page{
	Name: "clusters", Pattern: "GET /clusters", Title: "Clusters", Nav: "clusters",
	Params: []string{"realm"}, Fragments: []string{"pg-clusters-table"},
	Load: func(v *View) error {
		_, err := pgLoadClusters(v)
		return err
	},
}

// pgNodesPage is the Nodes page, served like the Clusters page.
var pgNodesPage = Page{
	Name: "nodes", Pattern: "GET /nodes", Title: "Nodes", Nav: "nodes",
	Params: []string{"realm"}, Fragments: []string{"pg-nodes-table"},
	Load: func(v *View) error {
		_, err := pgLoadNodes(v)
		return err
	},
}

// pgListTopics are what the list pages depend on.
var pgListTopics = []string{"realms", "clusters/", "nodes/", "monitoring/", "docker", "run"}

func pgClustersFragment() Fragment {
	return Fragment{
		Name: "pg-clusters-table", Params: []string{"realm"}, Prefixes: pgListTopics,
		Load: func(v *View) (err error) { v.Data, err = pgLoadClusters(v); return },
	}
}

func pgNodesFragment() Fragment {
	return Fragment{
		Name: "pg-nodes-table", Params: []string{"realm"}, Prefixes: pgListTopics,
		Load: func(v *View) (err error) { v.Data, err = pgLoadNodes(v); return },
	}
}

// pgClusterList is what the Clusters page shows.
type pgClusterList struct {
	// Base is the address of the page, which the realm filter links to.
	Base     string
	Realm    string
	Realms   []pgRealmRow
	Clusters []pgCluster
	Actions  []pgAction
}

// pgNodeList is what the Nodes page shows.
type pgNodeList struct {
	Base   string
	Realm  string
	Realms []pgRealmRow
	Nodes  []pgNode
}

// pgRealmRows returns the realms with their counts, the one named realm lit.
func pgRealmRows(sn store.Snapshot, realm string) []pgRealmRow {
	var rows []pgRealmRow
	for _, r := range pgRealmNames(sn, "") {
		rows = append(rows, pgRealmRow{
			Name: r, Clusters: len(sn.ClustersOf(r)), Nodes: len(sn.NodesOf(r)),
			Locked: sn.Lock(r) != nil, On: r == realm,
		})
	}
	return rows
}

func pgLoadClusters(v *View) (*pgClusterList, error) {
	realm, err := pgRealmParam(v)
	if err != nil {
		return nil, err
	}
	d := &pgClusterList{Base: "/clusters", Realm: realm, Realms: pgRealmRows(v.Snap, realm), Clusters: pgClustersOf(v.Snap, realm, v.Now)}
	d.Actions = []pgAction{pgDialogAction("New cluster…", "primary", "create-cluster", "realm", realm)}
	return d, nil
}

func pgLoadNodes(v *View) (*pgNodeList, error) {
	realm, err := pgRealmParam(v)
	if err != nil {
		return nil, err
	}
	d := &pgNodeList{Base: "/nodes", Realm: realm, Realms: pgRealmRows(v.Snap, realm)}
	for _, c := range pgClustersOf(v.Snap, realm, v.Now) {
		d.Nodes = append(d.Nodes, c.Nodes...)
	}
	return d, nil
}
