// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
)

// What the Not found page offers when a cluster, a node, a tab or a realm
// of the main pages is missing: the closest matches first, then the list
// that holds what was asked for.

// pgMissGone is the hint of a Miss that found nothing close.
const pgMissGone = "Someone may have deleted it, or the address has a typo."

// pgClusterMiss says that a realm has no cluster of a name. It offers the
// clusters of that name in other realms, the clusters of the realm whose
// names are close to it, and the list of clusters. A node, when not
// empty, is what the address asked for in that cluster: a link goes to
// that node where the other cluster has it.
func pgClusterMiss(sn store.Snapshot, realm, name, node string) *Miss {
	realms := pgRealmNames(sn, "")
	known := slices.Contains(realms, realm)
	m := &Miss{What: fmt.Sprintf("Realm %s has no cluster %s.", realm, name)}
	if !known {
		m.What = fmt.Sprintf("There is no realm %s.", realm)
	}
	link := func(r, c string) MissLink {
		if node != "" && pgHasNode(sn, r, c, node) {
			return MissLink{Label: pgArg(c, node), URL: pgNodeURL(r, c, node), Note: "in realm " + r}
		}
		return MissLink{Label: c, URL: pgClusterURL(r, c, ""), Note: "in realm " + r}
	}
	for _, r := range realms {
		if _, ok := sn.Cluster(r, name); ok && r != realm {
			m.Links = append(m.Links, link(r, name))
		}
	}
	var names []string
	for _, c := range sn.ClustersOf(realm) {
		names = append(names, c.Name)
	}
	for _, c := range missClosest(name, names, 3) {
		m.Links = append(m.Links, link(realm, c))
	}
	if !known {
		for _, r := range missClosest(realm, realms, 2) {
			m.Links = append(m.Links, MissLink{Label: "Clusters of realm " + r, URL: "/clusters?realm=" + url.QueryEscape(r)})
		}
	}
	if len(m.Links) == 0 {
		m.Hint = pgMissGone
	}
	if known {
		m.Links = append(m.Links, MissLink{Label: "Clusters of realm " + realm, URL: "/clusters?realm=" + url.QueryEscape(realm)})
	} else {
		m.Links = append(m.Links, MissLink{Label: "All clusters", URL: "/clusters?realm="})
	}
	return m
}

// pgHasNode reports whether the cluster of the realm has the node short.
func pgHasNode(sn store.Snapshot, realm, name, short string) bool {
	container := string(cluster.ContainerName(realm, name, short))
	return slices.ContainsFunc(sn.NodesOf(realm), func(n cluster.NodeSummary) bool { return n.Container == container })
}

// pgNodeMiss says that a cluster has no node of a name, and offers the
// nodes whose names are close to it and the cluster's Nodes tab. A cluster
// that is missing too is a pgClusterMiss.
func pgNodeMiss(sn store.Snapshot, realm, name, short string) *Miss {
	nodes := sn.ClusterNodes(realm, name)
	if _, ok := sn.Cluster(realm, name); !ok && len(nodes) == 0 {
		return pgClusterMiss(sn, realm, name, short)
	}
	m := &Miss{What: fmt.Sprintf("Cluster %s has no node %s.", name, short)}
	var shorts []string
	for _, n := range nodes {
		shorts = append(shorts, pgShort(realm, name, n.Container))
	}
	for _, s := range missClosest(short, shorts, 3) {
		m.Links = append(m.Links, MissLink{Label: pgArg(name, s), URL: pgNodeURL(realm, name, s)})
	}
	if len(m.Links) == 0 {
		m.Hint = pgMissGone
	}
	m.Links = append(m.Links, MissLink{Label: "Nodes of " + name, URL: pgClusterURL(realm, name, "nodes")})
	return m
}

// pgTabAliases are names that people try for a tab of the cluster page.
var pgTabAliases = map[string]string{
	"storage": "network", "volumes": "network", "mounts": "network", "networks": "network", "net": "network",
	"shell": "connect", "ssh": "connect", "log": "logs", "journal": "logs",
	"health": "summary", "overview": "summary", "workers": "nodes",
}

// pgTabMiss says that the cluster page has no tab of a name, and offers
// its tabs, the one meant most likely first.
func pgTabMiss(realm, name, tab string) *Miss {
	m := &Miss{What: fmt.Sprintf("The page of cluster %s has no tab %s.", name, tab)}
	keys := make([]string, len(pgTabs))
	for i, t := range pgTabs {
		keys[i] = t.Key
	}
	want := strings.ToLower(tab)
	first, ok := pgTabAliases[want]
	if c := missClosest(want, keys, 1); !ok && len(c) > 0 {
		first = c[0]
	}
	var rest []MissLink
	for _, t := range pgTabs {
		l := MissLink{Label: t.Label, URL: pgClusterURL(realm, name, t.Key)}
		if t.Key == first {
			m.Links = append(m.Links, l)
		} else {
			rest = append(rest, l)
		}
	}
	m.Links = append(m.Links, rest...)
	return m
}

// pgRealmMiss says that there is no realm of a name, on the page at base
// that shows one realm or all of them, and offers the realms whose names
// are close to it and the page with every realm.
func pgRealmMiss(sn store.Snapshot, base, realm string) *Miss {
	m := &Miss{What: fmt.Sprintf("There is no realm %s.", realm)}
	for _, r := range missClosest(realm, pgRealmNames(sn, ""), 3) {
		m.Links = append(m.Links, MissLink{Label: "Realm " + r, URL: base + "?realm=" + url.QueryEscape(r)})
	}
	if len(m.Links) == 0 {
		m.Hint = "Someone may have deleted it with its last cluster."
	}
	m.Links = append(m.Links, MissLink{Label: "All realms", URL: base + "?realm="})
	return m
}

// pgClusterByNameRoute serves /clusters/{cluster}, an address that leaves
// out the realm: it goes to the cluster when one realm has a cluster of
// that name, and else lists the realms that have one, or the clusters
// with a name close to it.
func pgClusterByNameRoute() Route {
	return Route{Pattern: "GET /clusters/{cluster}", Handle: func(s *Server, w http.ResponseWriter, r *http.Request) {
		v, err := s.View(r)
		if err != nil {
			s.Fail(w, r, err)
			return
		}
		name := v.Params.Get("cluster")
		var links []MissLink
		var all []string
		realmOf := map[string][]string{}
		for _, rn := range pgRealmNames(v.Snap, "") {
			for _, c := range v.Snap.ClustersOf(rn) {
				if c.Name == name {
					links = append(links, MissLink{Label: c.Name, URL: pgClusterURL(rn, c.Name, ""), Note: "in realm " + rn})
				}
				if len(realmOf[c.Name]) == 0 {
					all = append(all, c.Name)
				}
				realmOf[c.Name] = append(realmOf[c.Name], rn)
			}
		}
		if len(links) == 1 {
			http.Redirect(w, r, links[0].URL, http.StatusFound)
			return
		}
		isRealm := slices.Contains(pgRealmNames(v.Snap, ""), name)
		m := &Miss{What: fmt.Sprintf("No realm has a cluster %s.", name),
			Hint: "The address of a cluster is /clusters/REALM/CLUSTER."}
		switch {
		case len(links) > 1:
			m.What = fmt.Sprintf("More than one realm has a cluster %s.", name)
		case isRealm:
			m.What = fmt.Sprintf("The address names realm %s, but no cluster.", name)
		default:
			for _, c := range missClosest(name, all, 3) {
				for _, rn := range realmOf[c] {
					links = append(links, MissLink{Label: c, URL: pgClusterURL(rn, c, ""), Note: "in realm " + rn})
				}
			}
		}
		if isRealm {
			links = append(links, pgRealmListLink("clusters", name))
		}
		m.Links = append(links, MissLink{Label: "All clusters", URL: "/clusters?realm="})
		s.Fail(w, r, m)
	}}
}

// pgRealmListLink returns the link to the list of clusters or nodes, by
// the list's path, of one realm.
func pgRealmListLink(list, realm string) MissLink {
	label := "Clusters of realm "
	if list == "nodes" {
		label = "Nodes of realm "
	}
	return MissLink{Label: label + realm, URL: "/" + list + "?realm=" + url.QueryEscape(realm)}
}

// pgPathLinks returns what an address below /clusters/ or /nodes/ names
// when no route serves it, as with a slash at the end or more parts: the
// node and the cluster that it names, or the clusters of its realm whose
// names are close to the one it names, and that realm's list.
func pgPathLinks(sn store.Snapshot, segs []string) []MissLink {
	if len(segs) < 2 {
		return nil
	}
	list, realm := strings.ToLower(segs[0]), segs[1]
	if list != "clusters" && list != "nodes" || !slices.Contains(pgRealmNames(sn, ""), realm) {
		return nil
	}
	if len(segs) == 2 {
		return []MissLink{pgRealmListLink(list, realm)}
	}
	name := segs[2]
	var links []MissLink
	if _, ok := sn.Cluster(realm, name); !ok && len(sn.ClusterNodes(realm, name)) == 0 {
		var names []string
		for _, c := range sn.ClustersOf(realm) {
			names = append(names, c.Name)
		}
		for _, c := range missClosest(name, names, 3) {
			links = append(links, MissLink{Label: c, URL: pgClusterURL(realm, c, ""), Note: "in realm " + realm})
		}
		return append(links, pgRealmListLink(list, realm))
	}
	for _, short := range slices.Backward(segs[3:]) {
		if pgHasNode(sn, realm, name, short) {
			links = append(links, MissLink{Label: pgArg(name, short), URL: pgNodeURL(realm, name, short)})
			break
		}
	}
	if list == "nodes" {
		return append(links, MissLink{Label: "Nodes of " + name, URL: pgClusterURL(realm, name, "nodes")})
	}
	return append(links, MissLink{Label: name, URL: pgClusterURL(realm, name, ""), Note: "in realm " + realm})
}
