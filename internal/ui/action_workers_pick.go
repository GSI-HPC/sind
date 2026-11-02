// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

// Add workers asks for the cluster first when its link names none, as the
// Overview and the jump box do while there are several: a list of the
// clusters by realm, each with its health and its workers, of which one
// that a command creates or deletes now is off. A link that names the
// cluster shows it as a row with Change, which goes back to the list.

// actClusterRow is a cluster that Add workers can pick, or the one it
// adds to.
type actClusterRow struct {
	Realm, Name string
	Pill        pgPill
	// Workers says how many workers the cluster has, and their size as
	// sinfo reports that of the newest: "4 workers, 2 CPUs and 1 GiB
	// each".
	Workers string
	// Off says why workers cannot be added now, "" when they can.
	Off string
	// URL opens the dialog for the cluster.
	URL string
}

// actRealmRows are the clusters of one realm in the list.
type actRealmRows struct {
	Realm string
	Rows  []actClusterRow
}

// actClusterRowOf returns the cluster c of the list. The size of its
// workers is that of the worker that new ones copy, as sinfo reports it.
func actClusterRowOf(sn store.Snapshot, c pgCluster) actClusterRow {
	row := actClusterRow{Realm: c.Realm, Name: c.Name, Pill: c.Pill(), URL: pgDialog("create-worker", "realm", c.Realm, "cluster", c.Name)}
	workers := pgCount(c.Nodes, pgNode.Worker)
	row.Workers = pgPlural(workers, "worker", "workers")
	if newest, ok := action.NewestWorker(sn.ClusterNodes(c.Realm, c.Name), sn.Status(c.Realm, c.Name)); ok {
		if i := slices.IndexFunc(c.Nodes, func(n pgNode) bool { return n.Short == action.ShortName(newest) && n.Slurm != nil }); i >= 0 {
			each := " each"
			if workers == 1 {
				each = ""
			}
			row.Workers += fmt.Sprintf(", %s and %s%s", pgPlural(c.Nodes[i].Slurm.CPUs, "CPU", "CPUs"), pgMemory(c.Nodes[i].Slurm.MemoryMB), each)
		}
	}
	if c.Life != nil {
		row.Off = "sind deletes it now."
		if c.Life.Kind == pgCreating {
			row.Off = "sind creates it now. Add workers when the create ends."
		}
	}
	return row
}

// ClusterGroups are the clusters of the list by realm, the realm of the
// link first.
func (d *actDialog) ClusterGroups() []actRealmRows {
	var out []actRealmRows
	for _, c := range pgClustersOf(d.Snap, "", d.Now) {
		if len(out) == 0 || out[len(out)-1].Realm != c.Realm {
			out = append(out, actRealmRows{Realm: c.Realm})
		}
		out[len(out)-1].Rows = append(out[len(out)-1].Rows, actClusterRowOf(d.Snap, c))
	}
	later := func(g actRealmRows) int {
		if g.Realm == d.Realm {
			return 0
		}
		return 1
	}
	slices.SortStableFunc(out, func(a, b actRealmRows) int { return later(a) - later(b) })
	return out
}

// ClusterRow is the cluster of the dialog as a row, nil when sind ui does
// not know it.
func (d *actDialog) ClusterRow() *actClusterRow {
	s, ok := d.Snap.Cluster(d.Realm, d.Cluster)
	if !ok {
		return nil
	}
	row := actClusterRowOf(d.Snap, pgClusterOf(d.Snap, d.Realm, s, d.Snap.Monitorings[d.Realm], d.Now))
	return &row
}

// CanChange says that there is another cluster to pick instead.
func (d *actDialog) CanChange() bool {
	n := 0
	for _, cs := range d.Snap.Clusters {
		n += len(cs)
	}
	return n > 1
}

// ChangeURL opens the list of the clusters again, with the fields that the
// user filled in.
func (d *actDialog) ChangeURL() string { return pgDialog("create-worker", "realm", d.Realm) }

// actPickTitle and actPickLead are the heading and the lead of Add workers
// while it asks for the cluster.
const (
	actPickTitle = "Add workers"
	actPickLead  = "Pick the cluster first. New workers take the image, CPUs and memory of its newest worker, unless you change them."
)

// actNoCluster reports whether the link of a dialog that can ask for its
// cluster names none.
func actNoCluster(spec actSpec, f action.Form) bool {
	return spec.PickCluster && strings.TrimSpace(f.Get("cluster")) == ""
}
