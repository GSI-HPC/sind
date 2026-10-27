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
// page (pgNode.Problem), or a dot when every node is fine.
func pgNavCounts(r *Registry) {
	r.SetNavCount("clusters", func(sn store.Snapshot, _ time.Time) NavCount {
		n := 0
		for _, cs := range sn.Clusters {
			n += len(cs)
		}
		if n == 0 {
			return NavCount{}
		}
		return NavCount{Text: strconv.Itoa(n), Label: pgPlural(n, "cluster", "clusters")}
	})
	r.SetNavCount("nodes", func(sn store.Snapshot, _ time.Time) NavCount {
		nodes := pgSnapNodes(sn, "")
		problems := 0
		for _, n := range nodes {
			if n.Problem() {
				problems++
			}
		}
		switch {
		case problems > 0:
			return NavCount{Text: strconv.Itoa(problems) + " down", Class: "bad", Label: pgPlural(problems, "node needs", "nodes need") + " a look"}
		case len(nodes) > 0:
			return NavCount{Class: "ok", Label: "every node runs"}
		}
		return NavCount{}
	})
}
