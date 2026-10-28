// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"cmp"
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
	{"overview", "Overview", "/", "Overview", true, "o"},
	{"clusters", "Clusters", "/clusters", "Realm", true, "c"},
	{"nodes", "Nodes", "/nodes", "Realm", true, "n"},
	{"mesh", "Mesh and DNS", "/mesh", "Realm", false, "m"},
	{"monitoring", "Monitoring", "/monitoring", "Realm", false, "p"},
	{"topology", "Topology", "/topology", "Realm", false, "t"},
	{"doctor", "Doctor", "/doctor", "Host", true, "d"},
	{"activity", "Activity", "/activity", "Host", false, "a"},
}

// railData is what the rail fragment draws: the groups of entries, and for
// a phone the More entry and its sheet.
type railData struct {
	Groups []navGroup
	More   navMore
	// Sheet holds the entries that are not Main, for the More sheet.
	Sheet []navEntry
}

// navGroup is a labelled list of the rail and the entries in it.
type navGroup struct {
	// ID is the id of the group's label, which names its list.
	ID, Title string
	// Main is how many of its entries are Main, which sets its share of
	// the phone's bar.
	Main  int
	Items []navEntry
	// AllRealms is set when a count of its entries spans more than one
	// realm (NavCount.AllRealms), which its label says.
	AllRealms bool
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
// entry has the count that SetNavCount gave it.
func (r *Registry) rail(v *View) railData {
	icons := v.Asset("icons.svg") + "#"
	d := railData{More: navMore{Icon: icons + "more"}}
	for _, it := range navItems {
		e := navEntry{NavItem: it, On: it.Key == v.Nav, Icon: icons + it.Key}
		if fn := r.navCounts[it.Key]; fn != nil {
			e.Count = fn(v.Snap, v.Now)
		}
		if !it.Main {
			d.Sheet = append(d.Sheet, e)
			d.More.On = d.More.On || e.On
			d.More.note(e)
		}
		i := len(d.Groups) - 1
		if i < 0 || d.Groups[i].Title != it.Group {
			d.Groups = append(d.Groups, navGroup{ID: "rail-" + strings.ToLower(it.Group), Title: it.Group})
			i++
		}
		d.Groups[i].Items = append(d.Groups[i].Items, e)
		d.Groups[i].AllRealms = d.Groups[i].AllRealms || e.Count.AllRealms
		if it.Main {
			d.Groups[i].Main++
		}
	}
	return d
}
