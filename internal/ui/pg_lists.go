// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"slices"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

func init() { pgRegister(defaultRegistry) }

// pgRegister adds the main pages of sind ui to r: their pages, fragments,
// routes, and the counts of the rail.
func pgRegister(r *Registry) {
	for _, f := range slices.Concat(pgOverviewFragments(), pgPageFragments(pgClustersPage, pgListTopics), pgPageFragments(pgNodesPage, pgListTopics), pgClusterFragments(), pgNodeFragments()) {
		r.AddFragment(f)
	}
	// The list pages read the realm cookie, which a Page cannot see, so
	// they are routes, as the Overview is; the pages of one cluster or node
	// are pages.
	r.AddRoute(pgRealmRoute(pgClustersPage, true, false))
	r.AddRoute(pgRealmRoute(pgNodesPage, true, false))
	r.AddPage(pgClusterPage)
	r.AddRoute(pgClusterByNameRoute())
	r.AddPage(pgNodePage)
	pgNavCounts(r)
}

// pgClustersPage is the Clusters page. It is served by pgRealmRoute, which
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

// pgPageFragments returns the fragments of a page, which all read its
// Params, show what it loads and depend on the topics of the prefixes.
func pgPageFragments(p Page, prefixes []string) []Fragment {
	var out []Fragment
	for _, name := range p.Fragments {
		out = append(out, Fragment{Name: name, Params: p.Params, Prefixes: prefixes, Load: p.Load})
	}
	return out
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
func pgRealmRows(sn store.Snapshot, realm string, now time.Time) []pgRealmRow {
	var rows []pgRealmRow
	for _, r := range pgRealmNames(sn, "") {
		row := pgRealmRow{
			Name: r, Clusters: len(sn.Clusters[r]), Nodes: len(sn.Nodes[r]),
			Locked: sn.Locks[r] != nil, On: r == realm,
			Delete: pgDialogAction("Delete realm…", "sm danger", "delete-realm", "realm", r),
		}
		for _, c := range sn.Clusters[r] {
			if l := pgWorkOf(sn, r, c.Name, now).Life; l != nil {
				row.Lives = append(row.Lives, pgPill{l.Kind + " " + c.Name, "run"})
			}
		}
		row.Look, row.LookClass = pgLook(pgSnapNodes(sn, r, now))
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
	d := &pgClusterList{pgList: pgList{pgListQuery: q, Realms: pgRealmRows(v.Snap, q.Realm, v.Now), Cols: pgCols(q, pgClusterCols), Total: len(all)}}
	d.Chips = pgChips(q, pgClusterChips, func(key string) int {
		return pgCount(all, func(c pgCluster) bool { return pgClusterIn(c, key) })
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
		if l := c.Life; l != nil && l.Kind == pgCreating && l.Stop != nil {
			stop := *l.Stop
			stop.CSRF, stop.Small, stop.Label = v.CSRF, true, ""
			row.Stop = &stop
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
	d := &pgNodeList{pgList: pgList{pgListQuery: q, Realms: pgRealmRows(v.Snap, q.Realm, v.Now), Cols: pgCols(q, pgNodeCols), Total: len(all)}}
	d.Chips = pgChips(q, pgNodeChips, func(key string) int {
		return pgCount(all, func(n pgNode) bool { return pgNodeIn(n, key) })
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
