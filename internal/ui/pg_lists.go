// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"slices"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

func init() { pgRegister(defaultRegistry) }

// pgRegister adds the main pages of sind ui to r: their pages, fragments,
// routes, and the counts of the rail.
func pgRegister(r *Registry) {
	for _, f := range pgOverviewFragments() {
		r.AddFragment(f)
	}
	for _, f := range append(pgClustersFragments(), pgNodesFragments()...) {
		r.AddFragment(f)
	}
	for _, f := range pgClusterFragments() {
		r.AddFragment(f)
	}
	for _, f := range pgNodeFragments() {
		r.AddFragment(f)
	}
	// The list pages read the realm cookie, which a Page cannot see, so
	// they are routes, as the Overview is; the pages of one cluster or node
	// are pages.
	r.AddRoute(pgListRoute(pgClustersPage))
	r.AddRoute(pgListRoute(pgNodesPage))
	r.AddPage(pgClusterPage)
	r.AddRoute(pgClusterByNameRoute())
	r.AddPage(pgNodePage)
	pgNavCounts(r)
}

// pgClustersPage is the Clusters page. It is served by pgListRoute, which
// builds its handler from this value, so the registry holds no page of it.
// Its head, its chips and its table are fragments; the filter field is not,
// so that a live update never changes it while the user types.
var pgClustersPage = Page{
	Name: "clusters", Pattern: "GET /clusters", Title: "Clusters", Nav: "clusters",
	Params: pgListKeys, Fragments: []string{"pg-clusters-head", "pg-clusters-chips", "pg-clusters-table"},
	Load: func(v *View) (err error) { v.Data, err = pgLoadClusters(v); return },
}

// pgNodesPage is the Nodes page, served like the Clusters page.
var pgNodesPage = Page{
	Name: "nodes", Pattern: "GET /nodes", Title: "Nodes", Nav: "nodes",
	Params: pgListKeys, Fragments: []string{"pg-nodes-head", "pg-nodes-chips", "pg-nodes-table"},
	Load: func(v *View) (err error) { v.Data, err = pgLoadNodes(v); return },
}

// pgListTopics are what the list pages depend on.
var pgListTopics = []string{"realms", "clusters/", "nodes/", "status/", "monitoring/", "docker", "run"}

// pgListFragments returns the fragments of a list page, which all show
// what load returns.
func pgListFragments(prefix string, load func(v *View) (any, error)) []Fragment {
	var out []Fragment
	for _, part := range []string{"head", "chips", "table"} {
		out = append(out, Fragment{
			Name: prefix + "-" + part, Params: pgListKeys, Prefixes: pgListTopics,
			Load: func(v *View) (err error) { v.Data, err = load(v); return },
		})
	}
	return out
}

func pgClustersFragments() []Fragment {
	return pgListFragments("pg-clusters", func(v *View) (any, error) { return pgLoadClusters(v) })
}

func pgNodesFragments() []Fragment {
	return pgListFragments("pg-nodes", func(v *View) (any, error) { return pgLoadNodes(v) })
}

// pgList is what a list page shows besides its rows.
type pgList struct {
	pgListQuery
	Realms []pgRealmRow
	Chips  []pgChip
	Cols   []pgCol
	// Total counts the rows of the realm, Shown those that the filters
	// keep.
	Total, Shown int
}

// pgFilterView is what the filter field of a list renders from.
type pgFilterView struct {
	List               pgList
	Placeholder, Label string
}

// Filter returns the view of the list's filter field.
func (l pgList) Filter(placeholder, label string) pgFilterView {
	return pgFilterView{List: l, Placeholder: placeholder, Label: label}
}

// pgNoneView is what the row renders from that says that no row matches.
type pgNoneView struct {
	pgList
	// Noun names a row, as "cluster".
	Noun string
}

// None returns the view of the list's row for no match.
func (l pgList) None(noun string) pgNoneView { return pgNoneView{pgList: l, Noun: noun} }

// ClearURL returns the address of the list without its filters.
func (l pgList) ClearURL() string { return l.URL("q", "", "state", "") }

// pgClusterList is what the Clusters page shows.
type pgClusterList struct {
	pgList
	Clusters []pgClusterRow
	Actions  []pgAction
}

// pgNodeList is what the Nodes page shows.
type pgNodeList struct {
	pgList
	Nodes []pgNodeRow
}

// pgRealmRows returns the realms with their counts, the one named realm lit.
func pgRealmRows(sn store.Snapshot, realm string) []pgRealmRow {
	var rows []pgRealmRow
	for _, r := range pgRealmNames(sn, "") {
		row := pgRealmRow{
			Name: r, Clusters: len(sn.Clusters[r]), Nodes: len(sn.Nodes[r]), Problems: pgProblemNodes(sn, r),
			Locked: sn.Locks[r] != nil, On: r == realm,
			Delete: pgDialogAction("Delete realm…", "sm danger", "delete-realm", "realm", r),
		}
		row.Delete.Name = "Delete realm " + r + "…"
		rows = append(rows, row)
	}
	return rows
}

func pgLoadClusters(v *View) (*pgClusterList, error) {
	q, err := pgListQueryOf(v, "/clusters", "name", pgClusterChips, pgClusterCols)
	if err != nil {
		return nil, err
	}
	all := pgClustersOf(v.Snap, q.Realm, v.Now)
	d := &pgClusterList{pgList: pgList{pgListQuery: q, Realms: pgRealmRows(v.Snap, q.Realm), Cols: pgCols(q, pgClusterCols), Total: len(all)}}
	d.Chips = pgChips(q, pgClusterChips, func(key string) int {
		return len(slices.DeleteFunc(slices.Clone(all), func(c pgCluster) bool { return !pgClusterIn(c, key) }))
	})
	words := q.words()
	for _, c := range all {
		if !pgClusterIn(c, q.State) {
			continue
		}
		row := pgClusterRow{pgCluster: c, Text: pgClusterText(c), Label: c.Name}
		if q.Realm == "" {
			row.Label = c.Name + " (" + c.Realm + ")"
		}
		row.Hidden = !pgMatch(row.Text, words)
		if !row.Hidden {
			d.Shown++
		}
		d.Clusters = append(d.Clusters, row)
	}
	by := pgClusterCmp(q.Sort)
	pgOrder(d.Clusters, q.Desc, func(a, b pgClusterRow) int { return by(a.pgCluster, b.pgCluster) })
	d.Actions = []pgAction{pgDialogAction("New cluster…", "primary", "create-cluster", "realm", q.Realm)}
	return d, nil
}

func pgLoadNodes(v *View) (*pgNodeList, error) {
	q, err := pgListQueryOf(v, "/nodes", "cluster", pgNodeChips, pgNodeCols)
	if err != nil {
		return nil, err
	}
	var all []pgNode
	for _, c := range pgClustersOf(v.Snap, q.Realm, v.Now) {
		all = append(all, c.Nodes...)
	}
	d := &pgNodeList{pgList: pgList{pgListQuery: q, Realms: pgRealmRows(v.Snap, q.Realm), Cols: pgCols(q, pgNodeCols), Total: len(all)}}
	d.Chips = pgChips(q, pgNodeChips, func(key string) int {
		return len(slices.DeleteFunc(slices.Clone(all), func(n pgNode) bool { return !pgNodeIn(n, key) }))
	})
	words := q.words()
	for _, n := range all {
		if !pgNodeIn(n, q.State) {
			continue
		}
		row := pgNodeRow{pgNode: n, Text: pgNodeText(n)}
		row.Hidden = !pgMatch(row.Text, words)
		if !row.Hidden {
			d.Shown++
		}
		d.Nodes = append(d.Nodes, row)
	}
	by := pgNodeCmp(q.Sort)
	pgOrder(d.Nodes, q.Desc, func(a, b pgNodeRow) int { return by(a.pgNode, b.pgNode) })
	return d, nil
}
