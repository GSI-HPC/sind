// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

// The fleet table of the Overview: every cluster in one table, grouped by
// realm. A realm row gives what the realm holds and its health, with its
// ⋯ menu; a cluster row whether it is ready for jobs, its nodes as tiles,
// what is wrong in words or how far the command that creates or deletes it
// got, and its ⋯ menu.

// pgFleetChips are the chips of the fleet table: the ones of the Clusters
// list that say whether a cluster needs the user, or is being made.
var pgFleetChips = []pgChipDef{
	{Key: "", Label: "All"}, {Key: "problem", Label: "Needs a look"},
	{Key: pgCreating, Label: "Creating", Optional: true}, {Key: pgDeleting, Label: "Deleting", Optional: true},
}

// pgFleetIssues is how many nodes a row names in words; it counts the
// others.
const pgFleetIssues = 3

// pgFleetRealm is a realm of the fleet table: its row, and the rows of its
// clusters that the chip keeps.
type pgFleetRealm struct {
	pgRealmRow
	Rows []pgFleetRow
}

// MetaText says what the realm holds: "2 clusters · 6 nodes".
func (r pgFleetRealm) MetaText() string {
	return pgPlural(r.Clusters, "cluster", "clusters") + " · " + pgPlural(r.Nodes, "node", "nodes")
}

// pgFleetRow is a cluster of the fleet table.
type pgFleetRow struct {
	pgClusterRow
	// Ready answers whether the cluster can run a job now, ReadyText says
	// why, as "4 of 6 workers take jobs" (pgFleetReadyOf).
	Ready     pgPill
	ReadyText string
	// Issues are the nodes that need a look, in words; More counts those
	// that do not show, as "and 2 more". Note says what is wrong when no
	// node is, as "Docker has no volume sind-c-data, so /data is empty."
	Issues []pgFleetIssue
	More   string
	Note   string
	// NowNode and Now say what the command that creates or deletes the
	// cluster does now, as worker-0 and "waits for munge · 19s of 5m";
	// Now alone for the cluster as a whole, as "pull images".
	NowNode, Now string
}

// pgFleetIssue is a node that needs a look, with what is wrong with it in
// words, as "drained: memory errors".
type pgFleetIssue struct {
	Node pgNode
	Text string
}

// pgFleetOf builds the fleet table of the Overview: its chips, with the
// clusters that each keeps, and the realms that it shows with the clusters
// of each that the chip state keeps. A realm without clusters shows while
// no chip is on.
func pgFleetOf(d *pgOverview, sn store.Snapshot) {
	rows := make([]pgFleetRow, 0, len(d.Clusters))
	for _, c := range d.Clusters {
		rows = append(rows, pgFleetRowOf(sn, c, d.Realm == ""))
	}
	d.Chips = pgChips(pgListQuery{Base: "/", Realm: d.Realm, State: d.State}, pgFleetChips, func(key string) int {
		return pgCount(rows, func(r pgFleetRow) bool { return r.In(key) })
	})
	for i, c := range d.Chips {
		d.Chips[i].URL = pgOverviewURL(d.Realm, c.Key) + "#fleet"
	}
	for _, realm := range d.Realms {
		if d.Realm != "" && realm.Name != d.Realm {
			continue
		}
		fr := pgFleetRealm{pgRealmRow: realm}
		for _, r := range rows {
			if r.Realm == realm.Name && r.In(d.State) {
				fr.Rows = append(fr.Rows, r)
			}
		}
		if len(fr.Rows) > 0 || d.State == "" {
			d.Fleet = append(d.Fleet, fr)
		}
	}
}

// pgOverviewURL is the address of the Overview with the realm filter and
// the chip of the fleet table, each left out when it is off.
func pgOverviewURL(realm, state string) string {
	v := url.Values{}
	if realm != "" {
		v.Set("realm", realm)
	}
	if state != "" {
		v.Set("state", state)
	}
	if len(v) == 0 {
		return "/"
	}
	return "/?" + v.Encode()
}

// In reports whether the row is one of the chip's. A cluster needs a look
// when its pill says so, needs a look or not ready, so that the chip keeps
// the rows whose pill is amber or red.
func (r pgFleetRow) In(chip string) bool {
	if chip == "problem" {
		return r.Ready.Class == "warn" || r.Ready.Class == "bad"
	}
	return pgClusterIn(r.pgCluster, chip)
}

// pgFleetRowOf builds the row of a cluster of the fleet table.
func pgFleetRowOf(sn store.Snapshot, c pgCluster, allRealms bool) pgFleetRow {
	r := pgFleetRow{pgClusterRow: pgClusterRowOf(sn, c, allRealms, "")}
	// A create shows Show progress: the task dock has its Stop.
	r.Stop = nil
	var note string
	r.Ready, r.ReadyText, note = pgFleetReadyOf(sn, c)
	if l := c.Life; l != nil {
		r.NowNode, r.Now = pgLifeNow(l)
		return r
	}
	for _, n := range c.Nodes {
		if !n.Problem() {
			continue
		}
		if len(r.Issues) == pgFleetIssues {
			r.More = fmt.Sprintf("and %d more", pgCount(c.Nodes, pgNode.Problem)-pgFleetIssues)
			break
		}
		var words []string
		for _, i := range n.Issues() {
			words = append(words, i.Text)
		}
		text := strings.Join(words, ", ")
		if reason := n.SlurmReason(); reason != "" {
			text += ": " + reason
		}
		r.Issues = append(r.Issues, pgFleetIssue{Node: n, Text: text})
	}
	if len(r.Issues) == 0 {
		r.Note = note
	}
	return r
}

// pgLifeNow says what the command that creates or deletes a cluster does
// now: the node that it works on and where that stands, as worker-0 and
// "waits for munge · 19s of 5m"; else, for the cluster as a whole, that it
// waits for the lock of the realm, or its step.
func pgLifeNow(l *pgLife) (node, text string) {
	if i := slices.IndexFunc(l.Nodes, func(p pgPhase) bool { return p.State == runRunning }); i >= 0 {
		return l.Nodes[i].Node, l.Nodes[i].Text
	}
	if l.Run.Waiting {
		return "", "waits for the lock of the realm"
	}
	return "", l.Step
}

// pgFleetReadyOf answers in a pill whether the cluster can run a job now,
// by the checks of Ready for jobs on its Summary (pgReadyOf), and says why
// in a few words, with what is wrong in a sentence when no node is to
// blame. The pill is "ready"; "a command runs" while a command works on
// the cluster and no node needs a look by the rule of every page
// (pgCluster.NeedsLook): it may be starting what is down; "not ready" when
// it runs no job, "needs a look" (red or amber, as pgLook colours the count
// of the nodes that need a look) when it runs jobs but something is wrong;
// and "not checked yet" before the first probe of a cluster that looks
// fine. A command that creates or deletes it says how far it got.
func pgFleetReadyOf(sn store.Snapshot, c pgCluster) (pill pgPill, words, note string) {
	if l := c.Life; l != nil {
		pill, words = pgFleetLifeReady(l)
		return pill, words, ""
	}
	st := sn.Status(c.Realm, c.Name)
	d := &pgClusterView{Realm: c.Realm, Name: c.Name, Cluster: c, Status: st, Slurm: pgSlurmOf(sn.Slurm(c.Realm, c.Name), c.Nodes), Data: pgDataMount(st)}
	r := pgReadyOf(d)
	look := c.NeedsLook()
	needsLook := pgPill{"needs a look", "warn"}
	if n, class := pgLook(c.Nodes); n > 0 {
		// The colour of every count of the nodes that need a look: red
		// when a node does not run or a service is down.
		needsLook.Class = class
	}
	if !r.Checked {
		if look {
			return needsLook, "sind ui has not probed it yet", ""
		}
		return pgPill{"not checked yet", ""}, "sind ui has not probed it yet", ""
	}
	workers := pgWorkersText(r.Took, r.Of)
	if len(c.Runs) > 0 && !look {
		return pgPill{"a command runs", "run"}, workers, ""
	}
	notReady := pgPill{"not ready", "bad"}
	switch ctl, wk := r.Checks[0], r.Checks[1]; {
	case ctl.State == "bad":
		return notReady, ctl.Title, ctl.Detail
	case r.Of == 0:
		return notReady, workers, wk.Detail
	case r.Class == "bad":
		return notReady, workers, ""
	case look || wk.State == "warn":
		return needsLook, workers, ""
	}
	// The checks after the workers: accounting, and the files of jobs.
	for _, ch := range r.Checks[2:] {
		if ch.State == "warn" || ch.State == "bad" {
			return needsLook, ch.Title, ch.Detail
		}
	}
	return pgPill{"ready", "ok"}, workers, ""
}

// pgFleetLifeReady is the answer of pgFleetReadyOf while a command creates
// or deletes the cluster: "being created", with "nodes 1 of 3 ready", or
// "being deleted", with "8 of 11 nodes left".
func pgFleetLifeReady(l *pgLife) (pgPill, string) {
	kind, text := "being created", ""
	switch {
	case l.Kind == pgDeleting:
		kind = "being deleted"
		if len(l.Nodes) > 0 {
			left := pgCount(l.Nodes, func(p pgPhase) bool { return p.State != runOK })
			text = fmt.Sprintf("%d of %s left", left, pgPlural(len(l.Nodes), "node", "nodes"))
		}
	case l.Run.Waiting:
		text = "the create is queued"
	case len(l.Nodes) > 0:
		ready := pgCount(l.Nodes, func(p pgPhase) bool { return p.State == runOK })
		text = fmt.Sprintf("nodes %d of %d ready", ready, len(l.Nodes))
	}
	return pgPill{kind, "run"}, text
}

// pgWorkersText says how many workers take jobs: "4 of 6 workers take
// jobs", "1 of 2 workers takes jobs", "1 of 1 worker takes jobs", "no
// worker takes jobs", or "no worker" for a cluster without one.
func pgWorkersText(took, of int) string {
	switch {
	case of == 0:
		return "no worker"
	case took == 0:
		return "no worker takes jobs"
	}
	noun, verb := "workers", "take"
	if of == 1 {
		noun = "worker"
	}
	if took == 1 {
		verb = "takes"
	}
	return fmt.Sprintf("%d of %d %s %s jobs", took, of, noun, verb)
}

// NewCluster is the button of a realm without clusters: New cluster, in
// that realm.
func (r pgFleetRealm) NewCluster() pgAction {
	return pgDialogAction("New cluster…", "sm", "create-cluster", "realm", r.Name)
}
