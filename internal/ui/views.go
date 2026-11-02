// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"cmp"
	"net/url"
	"strings"
)

// NavItem is an entry of the rail.
type NavItem struct {
	// Key is what Page.Nav names, and the ID of the entry's icon in
	// web/static/icons.svg.
	Key   string
	Label string
	Href  string
	// Group is the label of the list it stands in.
	Group string
	// Main puts the entry in the bar at the bottom of a phone's screen;
	// the others are in the bar's More sheet.
	Main bool
	// Chord is the key that opens the page after g. help.js holds the
	// same list.
	Chord string
}

// navItems are the entries of the rail, in order. The pages that they link
// to come from other parts of sind ui.
var navItems = []NavItem{
	{"overview", "Overview", "/", navOverviewGroup, true, "o"},
	{"activity", "Activity", "/activity", navOverviewGroup, false, "a"},
	{"clusters", "Clusters", "/clusters", navRealmGroup, true, "c"},
	{"nodes", "Nodes", "/nodes", navRealmGroup, true, "n"},
	{"mesh", "Mesh and DNS", "/mesh", navRealmGroup, false, "m"},
	{"monitoring", "Monitoring", "/monitoring", navRealmGroup, false, "p"},
	{"topology", "Topology", "/topology", navRealmGroup, false, "t"},
	{"doctor", "Doctor", "/doctor", "Host", true, "d"},
}

// The groups of the rail that the realm scope concerns: the pages of the
// Overview group show every realm, those of the Realm group the realm of
// the scope (View.Scope), which the switcher at the head of the group
// picks.
const (
	navOverviewGroup = "Overview"
	navRealmGroup    = "Realm"
)

// navGroupOf returns the group of the rail entry key, "" for none.
func navGroupOf(key string) string {
	for _, it := range navItems {
		if it.Key == key {
			return it.Group
		}
	}
	return ""
}

// navLabels returns the labels of the entries of a group, in order.
func navLabels(group string) []string {
	var out []string
	for _, it := range navItems {
		if it.Group == group {
			out = append(out, it.Label)
		}
	}
	return out
}

// railData is what the rail fragment draws: the groups of entries, and for
// a phone the More entry and its sheet.
type railData struct {
	Groups []navGroup
	More   navMore
	// Sheet holds the entries that are not Main, for the More sheet.
	Sheet []navEntry
	// Realm is the realm switcher at the head of the Realm group, and at
	// the top of the More sheet; nil while no realm is known.
	Realm *railRealm
}

// navGroup is a labelled list of the rail and the entries in it.
type navGroup struct {
	// ID is the id of the group's label, which names its list.
	ID, Title string
	// Main is how many of its entries are Main, which sets its share of
	// the phone's bar.
	Main  int
	Items []navEntry
	// AllRealms is set on the Overview group when there is more than one
	// realm, which its label says: its pages show them all.
	AllRealms bool
	// Scope is set on the Realm group, whose head is the realm switcher.
	Scope bool
}

// navEntry is an entry of the rail as the template draws it.
type navEntry struct {
	NavItem
	On    bool
	Count NavCount
	// Icon is the URL of the entry's symbol in icons.svg.
	Icon string
}

// navMore is the More entry of a phone's bar. It is lit when the page is
// one of its sheet, and it shows the most urgent count of the sheet: a
// failure first, then work that goes on.
type navMore struct {
	On    bool
	Icon  string
	Count NavCount
	// Note says which entry the count comes from, for screen readers.
	Note string
}

// navUrgency ranks the classes of a count, and navMarks are what the More
// entry shows for them.
var (
	navUrgency = map[string]int{"run": 1, "bad": 2}
	navMarks   = map[string]string{"run": runMark(runRunning), "bad": "!"}
)

// note takes e's count when it is more urgent than the one More shows.
func (m *navMore) note(e navEntry) {
	if navUrgency[e.Count.Class] <= navUrgency[m.Count.Class] {
		return
	}
	m.Count = NavCount{Text: navMarks[e.Count.Class], Class: e.Count.Class}
	m.Note = e.Label + ": " + cmp.Or(e.Count.Label, e.Count.Text)
}

// rail returns the rail for the view: the entry v.Nav is lit, and each
// entry has the count that SetNavCount gave it. The entries of the Realm
// group count the realm of the scope, and link to it.
func (r *Registry) rail(v *View) railData {
	icons := v.Asset("icons.svg") + "#"
	d := railData{More: navMore{Icon: icons + "more"}, Realm: railRealmOf(v)}
	realms := len(pgRealmNames(v.Snap, ""))
	for _, it := range navItems {
		e := navEntry{NavItem: it, On: it.Key == v.Nav, Icon: icons + it.Key}
		scope := ""
		if it.Group == navRealmGroup && v.Scope != "" {
			scope = v.Scope
			e.Href += "?realm=" + url.QueryEscape(scope)
		}
		if fn := r.navCounts[it.Key]; fn != nil {
			e.Count = fn(v.Snap, scope, v.Now)
		}
		if !it.Main {
			d.Sheet = append(d.Sheet, e)
			d.More.On = d.More.On || e.On
			d.More.note(e)
		}
		i := len(d.Groups) - 1
		if i < 0 || d.Groups[i].Title != it.Group {
			d.Groups = append(d.Groups, navGroup{
				ID: "rail-" + strings.ToLower(it.Group), Title: it.Group,
				AllRealms: it.Group == navOverviewGroup && realms > 1, Scope: it.Group == navRealmGroup && d.Realm != nil,
			})
			i++
		}
		d.Groups[i].Items = append(d.Groups[i].Items, e)
		if it.Main {
			d.Groups[i].Main++
		}
	}
	return d
}
