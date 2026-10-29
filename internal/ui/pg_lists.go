// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"slices"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
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
	r.AddRoute(Route{Pattern: "POST /needs/runs/{id}/dismiss", Handle: pgDismissRun})
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
	// Realms are the names of every realm.
	Realms []string
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

// pgListScope is what a list holds, for the head of its page: "2 clusters
// in realm ci.", "5 clusters in 2 realms.", "1 cluster." with one realm.
type pgListScope struct {
	// Count is "2 clusters", Realm the realm of the list, and Realms
	// "2 realms" when the list shows more than one.
	Count, Realm, Realms string
}

// Scope returns what the list holds, counting its rows as one or many.
func (l pgList) Scope(one, many string) pgListScope {
	sc := pgListScope{Count: pgPlural(l.Total, one, many), Realm: l.Realm}
	if l.Realm == "" && len(l.Realms) > 1 {
		sc.Realms = pgPlural(len(l.Realms), "realm", "realms")
	}
	return sc
}

// Commands returns the commands that list in a terminal what the list
// holds, as "sind --realm ci get clusters": the one of its realm, or one
// for each realm, as sind get works on one realm.
func (l pgList) Commands(what string) []string {
	realms := l.Realms
	if l.Realm != "" || len(realms) == 0 {
		realms = []string{l.Realm}
	}
	out := make([]string, len(realms))
	for i, r := range realms {
		out[i] = pgSind(r) + " get " + what
	}
	return out
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

// AnyLife reports whether a command creates or deletes a node of a
// cluster of the list, whose tile the legend then explains.
func (d *pgClusterList) AnyLife() bool {
	return slices.ContainsFunc(d.Clusters, func(r pgClusterRow) bool { return r.AnyLife() })
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
		}
		for _, c := range sn.Clusters[r] {
			if l := pgWorkOf(sn, r, c.Name, now).Life; l != nil {
				row.Lives = append(row.Lives, pgPill{l.Kind + " " + c.Name, "run"})
			}
		}
		row.Look, row.LookClass = pgLook(pgSnapNodes(sn, r, now))
		row.Menu = pgMenu{ID: "realm", Label: "Actions for realm " + r, Icon: "⋯", Groups: []pgMenuGroup{
			{Label: "Mesh", Items: []pgMenuItem{{Label: "Connect a container…", Hint: "Let a container of yours reach the nodes by name.", URL: pgDialog("connect", "realm", r)}}},
			{Label: "Can lose data", Danger: true, Items: []pgMenuItem{{Label: "Delete realm…", Hint: pgRealmDeleteHint(sn, r), URL: pgDialog("delete-realm", "realm", r)}}},
		}}
		rows = append(rows, row)
	}
	return rows
}

// pgDataKinds returns the kinds of the volumes of the cluster that hold
// the data of its users, which a delete of it removes: "data", "home",
// both or none.
func pgDataKinds(sn store.Snapshot, realm, name string) []string {
	var kinds []string
	for _, t := range []cluster.VolumeType{cluster.VolumeData, cluster.VolumeHome} {
		vol := string(cluster.VolumeName(realm, name, t))
		if slices.ContainsFunc(sn.Volumes[realm], func(v cluster.VolumeSummary) bool { return v.Name == vol }) {
			kinds = append(kinds, string(t))
		}
	}
	return kinds
}

// pgDataVolumes names n volumes of the kinds: "data volume", "data and
// home volumes".
func pgDataVolumes(kinds []string, n int) string {
	if n == 1 {
		return pgJoin(kinds) + " volume"
	}
	return pgJoin(kinds) + " volumes"
}

// pgRealmDeleteHint is the hint of Delete realm… in the menu of a realm:
// its clusters go, and with them the files on their data and home
// volumes, if any; host directories stay.
func pgRealmDeleteHint(sn store.Snapshot, realm string) string {
	var kinds []string
	n := 0
	for _, c := range sn.Clusters[realm] {
		k := pgDataKinds(sn, realm, c.Name)
		n += len(k)
		for _, kind := range k {
			if !slices.Contains(kinds, kind) {
				kinds = append(kinds, kind)
			}
		}
	}
	if n == 0 {
		return "Every cluster of it goes, and its mesh."
	}
	slices.Sort(kinds)
	return "Every cluster of it goes, with the files on their " + pgDataVolumes(kinds, n) + "."
}

func pgLoadClusters(v *View) (*pgClusterList, error) {
	q, err := pgListQueryOf(v, "/clusters", "name", pgClusterChips, pgClusterCols)
	if err != nil {
		return nil, err
	}
	all := pgClustersOf(v.Snap, q.Realm, v.Now)
	d := &pgClusterList{pgList: pgList{pgListQuery: q, Realms: pgRealmNames(v.Snap, ""), Cols: pgCols(q, pgClusterCols), Total: len(all)}}
	d.Chips = pgChips(q, pgClusterChips, func(key string) int {
		return pgCount(all, func(c pgCluster) bool { return pgClusterIn(c, key) })
	})
	words := q.words()
	for _, c := range all {
		if !pgClusterIn(c, q.State) {
			continue
		}
		row := pgClusterRow{pgCluster: c, Text: pgClusterText(c), Label: c.Name, DataKinds: pgDataKinds(v.Snap, c.Realm, c.Name)}
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
	d := &pgNodeList{pgList: pgList{pgListQuery: q, Realms: pgRealmNames(v.Snap, ""), Cols: pgCols(q, pgNodeCols), Total: len(all)}}
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
