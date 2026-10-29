// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"cmp"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

func init() { defaultRegistry.AddFragment(railFragment()) }

// railFragment is the rail, the page list at the left of every page. It
// depends on every topic, as its counts come from any part of the snapshot;
// the server sends it only when its HTML differs.
func railFragment() Fragment {
	return Fragment{
		Name:     "rail",
		Prefixes: []string{""},
		Load:     func(v *View) error { v.Data = v.reg.rail(v); return nil },
	}
}

// pgNavCounts sets the badges of the rail entries of the main pages: the
// number of clusters, and the nodes that need a look by the rule of every
// page (pgNode.Problem), in the words and the class of every page (pgLook),
// or a dot when every node is fine. They count the realm of the scope, or
// every realm, and their label says which (pgScopeIn).
func pgNavCounts(r *Registry) {
	r.SetNavCount("clusters", func(sn store.Snapshot, realm string, _ time.Time) NavCount {
		n := 0
		for _, name := range pgRealmNames(sn, realm) {
			n += len(sn.Clusters[name])
		}
		if n == 0 {
			return NavCount{}
		}
		return NavCount{Text: strconv.Itoa(n), Label: pgPlural(n, "cluster", "clusters") + pgScopeIn(sn, realm)}
	})
	r.SetNavCount("nodes", func(sn store.Snapshot, realm string, now time.Time) NavCount {
		nodes := pgSnapNodes(sn, realm, now)
		problems, class := pgLook(nodes)
		in := pgScopeIn(sn, realm)
		switch {
		case problems > 0:
			return NavCount{Text: strconv.Itoa(problems), Unit: pgLookUnit(problems), Class: class, Label: pgLookText(problems) + in}
		case len(nodes) > 0:
			return NavCount{Class: "ok", Label: "every node" + in + " runs"}
		}
		return NavCount{}
	})
}

// pgScopeIn returns what a count of the rail spans, for its label: " in
// realm ci" for one realm, " in all realms" for every realm when there is
// more than one, else "".
func pgScopeIn(sn store.Snapshot, realm string) string {
	switch {
	case realm != "":
		return " in realm " + realm
	case len(pgRealmNames(sn, "")) > 1:
		return " in all realms"
	}
	return ""
}

// railRealm is the realm switcher of the rail: a button that shows the
// scope, and a menu of every realm with its health, whose entries go to
// the same page, or to the list of clusters, in that realm.
type railRealm struct {
	// Scope is the realm of the scope, "" for every realm, and Text what
	// it holds: "2 clusters".
	Scope, Text string
	// Look says which other realm needs a look, as "Realm sind: 3 nodes
	// need a look", for the dot on the button; LookClass is its class.
	Look, LookClass string
	Options         []railRealmOption
	// Note says which pages the scope concerns.
	Note string
	// ID is the prefix of the ids of a copy of the switcher.
	ID string
}

// railRealmOption is an entry of the menu of the realm switcher.
type railRealmOption struct {
	// Name is the realm, "" for every realm.
	Name, Text, URL string
	On              bool
	// Look is the class of the dot of a realm that needs a look.
	Look string
}

// Label returns the name of the option: the realm, or "All realms".
func (o railRealmOption) Label() string { return cmp.Or(o.Name, "All realms") }

// At returns the copy of the switcher whose ids start with id: the rail
// and the More sheet of a phone each have one.
func (r railRealm) At(id string) railRealm {
	r.ID = id
	return r
}

// Label returns what the button of the switcher says to a screen reader:
// "Realm: ci, 2 clusters. Realm sind: 3 nodes need a look."
func (r railRealm) Label() string {
	s := "Realm: " + cmp.Or(r.Scope, "all realms") + ", " + r.Text + "."
	if r.Look != "" {
		s += " " + r.Look + "."
	}
	return s
}

// railRealmOf returns the realm switcher of the view, nil while no realm is
// known. A scope that the snapshot does not hold, such as a realm that
// Docker has no trace of while it is down, is an entry of its own.
func railRealmOf(v *View) *railRealm {
	rows := pgRealmRows(v.Snap, v.Now)
	if len(rows) == 0 && v.Scope == "" {
		return nil
	}
	next := pgRealmNext(v) + "?realm="
	r := &railRealm{Scope: v.Scope, Note: pgJoin(navLabels(navRealmGroup)) + " show this realm. " +
		pgJoin(navLabels(navOverviewGroup)) + " show all realms."}
	all, known := 0, false
	var opts []railRealmOption
	for _, row := range rows {
		all += row.Clusters
		known = known || row.Name == v.Scope
		parts := []string{pgPlural(row.Clusters, "cluster", "clusters")}
		for _, l := range row.Lives {
			parts = append(parts, l.Text)
		}
		o := railRealmOption{Name: row.Name, URL: next + url.QueryEscape(row.Name), On: row.Name == v.Scope}
		if row.Look > 0 {
			parts = append(parts, pgLookShort(row.Look))
			o.Look = row.LookClass
			// The dot names the first other realm that needs a look, a bad
			// one before one that warns.
			if v.Scope != "" && !o.On && (r.Look == "" || o.Look == "bad" && r.LookClass != "bad") {
				r.Look, r.LookClass = "Realm "+row.Name+": "+pgLookText(row.Look), row.LookClass
			}
		}
		if o.On {
			r.Text = parts[0]
		}
		o.Text = strings.Join(parts, " · ")
		opts = append(opts, o)
	}
	r.Options = append([]railRealmOption{{URL: next, On: v.Scope == "", Text: pgPlural(all, "cluster", "clusters")}}, opts...)
	switch {
	case v.Scope == "":
		r.Text = r.Options[0].Text
	case !known:
		r.Text = "no cluster"
		r.Options = append(r.Options, railRealmOption{Name: v.Scope, URL: next + url.QueryEscape(v.Scope), On: true, Text: r.Text})
	}
	return r
}
