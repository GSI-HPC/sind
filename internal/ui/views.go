// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"github.com/GSI-HPC/sind/internal/ui/store"
)

// NavItem is an entry of the rail.
type NavItem struct {
	// Key is what Page.Nav names, and the ID of the entry's icon in
	// web/static/icons.svg.
	Key   string
	Label string
	Href  string
	// Group is the heading it stands under.
	Group string
}

// navItems are the entries of the rail, in order. The pages that they link
// to come from other parts of sind ui.
var navItems = []NavItem{
	{"overview", "Overview", "/", "Overview"},
	{"clusters", "Clusters", "/clusters", "Realm"},
	{"nodes", "Nodes", "/nodes", "Realm"},
	{"mesh", "Mesh and DNS", "/mesh", "Realm"},
	{"monitoring", "Monitoring", "/monitoring", "Realm"},
	{"topology", "Topology", "/topology", "Realm"},
	{"doctor", "Doctor", "/doctor", "Host"},
	{"activity", "Activity", "/activity", "Host"},
}

// navGroup is a heading of the rail and the entries below it.
type navGroup struct {
	Title string
	Items []navEntry
}

// navEntry is an entry of the rail as the template draws it.
type navEntry struct {
	NavItem
	On    bool
	Count NavCount
}

// rail returns the rail's groups for the view: the entry v.Nav is lit, and
// each entry has the count that SetNavCount gave it.
func (r *Registry) rail(v *View) []navGroup {
	var groups []navGroup
	for _, it := range navItems {
		e := navEntry{NavItem: it, On: it.Key == v.Nav}
		if fn := r.navCount(it.Key); fn != nil {
			e.Count = fn(v.Snap)
		}
		if i := len(groups) - 1; i >= 0 && groups[i].Title == it.Group {
			groups[i].Items = append(groups[i].Items, e)
			continue
		}
		groups = append(groups, navGroup{Title: it.Group, Items: []navEntry{e}})
	}
	return groups
}

// navCount returns the count function of the rail entry key, or nil.
func (r *Registry) navCount(key string) func(store.Snapshot) NavCount {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.navCounts[key]
}
