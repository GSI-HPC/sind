// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/internal/help"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

// jumpMax is the most entries that the jump box lists.
const jumpMax = 14

// jumpItem is an entry of the jump box: a link, or with Topic an entry of
// help that opens in the pane.
type jumpItem struct {
	Label string
	// Kind is the small text at the right: "cluster · sind", "word".
	Kind string
	Href string
	// Topic is set for help entries: the pane opens it.
	Topic string
	// Text is more to match, not shown.
	Text string
}

// jumpData is what the block help-jump-list renders from.
type jumpData struct {
	Query string
	// HelpOnly is set when the query starts with ?.
	HelpOnly bool
	Items    []jumpItem
}

// jumpHandle serves GET /jump?q=: the entries of the jump box that match q,
// as the list of the box. A query that starts with ? finds help only.
func jumpHandle(s *Server, w http.ResponseWriter, r *http.Request) {
	v, err := s.View(r, "q")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	v.Data = jump(help.Default(), v.Snap, v.Params.Get("q"))
	s.Write(w, r, http.StatusOK, "help-jump-list", v)
}

// jump returns the entries for q: the pages, the actions, the clusters and
// nodes of every realm, the latest runs, and help. Entries whose label
// starts with q come first, then those whose label holds it, then those
// whose text does.
func jump(h *help.Help, sn store.Snapshot, q string) *jumpData {
	q = strings.TrimSpace(q)
	rest, only := strings.CutPrefix(q, "?")
	d := &jumpData{Query: q, HelpOnly: only}
	q = strings.ToLower(strings.TrimSpace(rest))
	var items []jumpItem
	if !only {
		items = jumpObjects(h, sn)
	}
	for _, t := range h.Howtos() {
		items = append(items, jumpItem{t.Title, "how to", "", helpTopic(help.KindHowto, t.ID), t.Summary})
	}
	for _, w := range h.Words() {
		items = append(items, jumpItem{w.Title, "word", "", helpTopic(help.KindWord, w.ID), w.Summary})
	}
	if only {
		for _, a := range h.Actions() {
			items = append(items, jumpItem{a.Title, "action", "", helpTopic(help.KindAction, a.ID), a.Summary})
		}
	}
	type scored struct {
		jumpItem
		score int
	}
	var found []scored
	for _, it := range items {
		label := strings.ToLower(it.Label)
		switch {
		case q == "" || strings.HasPrefix(label, q):
			found = append(found, scored{it, 0})
		case strings.Contains(label, q):
			found = append(found, scored{it, 1})
		case strings.Contains(strings.ToLower(it.Kind+" "+it.Text), q):
			found = append(found, scored{it, 2})
		}
	}
	slices.SortStableFunc(found, func(a, b scored) int { return a.score - b.score })
	for _, f := range found {
		if len(d.Items) == jumpMax {
			break
		}
		d.Items = append(d.Items, f.jumpItem)
	}
	return d
}

// jumpObjects lists what the user can open: pages without a wildcard, the
// actions, and the clusters, nodes and runs of the snapshot.
func jumpObjects(h *help.Help, sn store.Snapshot) []jumpItem {
	var items []jumpItem
	for _, p := range h.Pages() {
		if !strings.Contains(p.Route, "{") {
			items = append(items, jumpItem{Label: p.Title, Kind: "page", Href: p.Route, Text: p.Summary})
		}
	}
	for _, a := range h.Actions() {
		items = append(items, jumpItem{Label: a.Title, Kind: "action", Href: "/dialogs/" + url.PathEscape(a.ID), Text: a.Summary})
	}
	for _, realm := range slices.Sorted(maps.Keys(sn.Clusters)) {
		for _, c := range sn.Clusters[realm] {
			items = append(items, jumpItem{Label: c.Name, Kind: "cluster · " + realm,
				Href: "/clusters/" + url.PathEscape(realm) + "/" + url.PathEscape(c.Name)})
		}
	}
	for _, realm := range slices.Sorted(maps.Keys(sn.Nodes)) {
		for _, n := range sn.Nodes[realm] {
			short, _, _ := strings.Cut(n.FQDN, ".")
			items = append(items, jumpItem{Label: short + "." + n.Cluster, Kind: "node · " + realm,
				Href: "/nodes/" + url.PathEscape(realm) + "/" + url.PathEscape(n.Cluster) + "/" + url.PathEscape(short)})
		}
	}
	for i, r := range sn.Runs {
		if i == 8 {
			break
		}
		status := r.Status
		if status == "" {
			status = "running"
		}
		items = append(items, jumpItem{Label: r.Title, Kind: "run · " + status, Href: "/runs/" + url.PathEscape(r.ID)})
	}
	return items
}
