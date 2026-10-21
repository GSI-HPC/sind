// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"strconv"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
)

func init() { defaultRegistry.AddFragment(railFragment()) }

// railFragment is the rail, the page list at the left of every page. It
// depends on every topic, as its counts come from any part of the snapshot;
// the server sends it only when its HTML differs.
func railFragment() Fragment {
	return Fragment{
		Name:     "rail",
		Prefixes: []string{""},
		Shell:    true,
		Load:     func(v *View) error { v.Data = v.reg.rail(v); return nil },
	}
}

// pgNavCounts sets the counts of the rail entries of the main pages: the
// number of clusters, and the number of nodes that are not running, which
// shows only when there are some.
func pgNavCounts(r *Registry) {
	r.SetNavCount("clusters", func(sn store.Snapshot) NavCount {
		n := 0
		for _, cs := range sn.Clusters {
			n += len(cs)
		}
		if n == 0 {
			return NavCount{}
		}
		return NavCount{Text: strconv.Itoa(n)}
	})
	r.SetNavCount("nodes", func(sn store.Snapshot) NavCount {
		n := 0
		for _, ns := range sn.Nodes {
			for _, x := range ns {
				if x.State != cluster.StateRunning {
					n++
				}
			}
		}
		if n == 0 {
			return NavCount{}
		}
		return NavCount{Text: fmt.Sprint(n), Class: "bad"}
	})
}
