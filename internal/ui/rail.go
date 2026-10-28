// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"strconv"
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
// or a dot when every node is fine. They count every realm, and say so in
// their label and in that of their group when there is more than one.
func pgNavCounts(r *Registry) {
	r.SetNavCount("clusters", func(sn store.Snapshot, _ time.Time) NavCount {
		n := 0
		for _, cs := range sn.Clusters {
			n += len(cs)
		}
		if n == 0 {
			return NavCount{}
		}
		in, all := pgAllRealms(sn)
		return NavCount{Text: strconv.Itoa(n), Label: pgPlural(n, "cluster", "clusters") + in, AllRealms: all}
	})
	r.SetNavCount("nodes", func(sn store.Snapshot, _ time.Time) NavCount {
		nodes := pgSnapNodes(sn, "")
		problems, class := pgLook(nodes)
		in, all := pgAllRealms(sn)
		switch {
		case problems > 0:
			return NavCount{Text: strconv.Itoa(problems), Unit: pgLookUnit(problems), Class: class, Label: pgLookText(problems) + in, AllRealms: all}
		case len(nodes) > 0:
			return NavCount{Class: "ok", Label: "every node" + in + " runs", AllRealms: all}
		}
		return NavCount{}
	})
}

// pgAllRealms returns " in all realms" and true when the snapshot has more
// than one realm, for the label of a count of the rail, which counts them
// all.
func pgAllRealms(sn store.Snapshot) (string, bool) {
	if len(pgRealmNames(sn, "")) > 1 {
		return " in all realms", true
	}
	return "", false
}
