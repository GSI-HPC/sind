// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"slices"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
)

// pgNodePage is the page of one node, template pg_node.tmpl.
var pgNodePage = Page{
	Name: "node", Pattern: "GET /nodes/{realm}/{cluster}/{node}", Title: "Node", Nav: "nodes",
	Fragments: []string{"pg-node-head", "pg-node-body"},
	Load: func(v *View) error {
		d := pgLoadNode(v)
		if !d.Found {
			return pgNodeMiss(v.Snap, d.Realm, d.Cluster, d.Short)
		}
		v.Title = d.Node.Arg
		return nil
	},
}

func pgNodeFragments() []Fragment {
	params := []string{"realm", "cluster", "node"}
	load := func(v *View) error { v.Data = pgLoadNode(v); return nil }
	return []Fragment{
		{Name: "pg-node-head", Params: params, Watch: pgClusterWatch, Load: load},
		{Name: "pg-node-body", Params: params, Watch: pgClusterWatch, Load: load},
	}
}

// pgNodeView is what a node page shows.
type pgNodeView struct {
	// Found is false when the snapshot knows no such node.
	Found                 bool
	Realm, Cluster, Short string
	Node                  pgNode
	// ClusterPill is the state pill of the node's cluster, as its pages
	// show it: "creating · nodes 1/3" while a command creates it.
	ClusterPill pgPill
	// Slurm is what Slurm says about the node's cluster.
	Slurm    *pgSlurmView
	Actions  []pgAction
	Commands []pgCmd
	// Run is the command that creates or deletes the node now, nil for
	// none; the node then has no actions.
	Run *pgRun
}

// pgServiceOf returns the Slurm service that a role runs.
func pgServiceOf(r config.Role) string {
	switch r {
	case config.RoleController:
		return "slurmctld"
	case config.RoleDB:
		return "slurmdbd"
	case config.RoleAPI:
		return "slurmrestd"
	case config.RoleSubmitter:
		return "sackd"
	}
	return "slurmd"
}

// pgLoadNode builds the view of a node page from the view's Params (realm,
// cluster, node).
func pgLoadNode(v *View) *pgNodeView {
	realm, name, short := v.Params.Get("realm"), v.Params.Get("cluster"), v.Params.Get("node")
	d := &pgNodeView{Realm: realm, Cluster: name, Short: short}
	container := string(cluster.ContainerName(realm, name, short))
	for _, ns := range v.Snap.ClusterNodes(realm, name) {
		if ns.Container != container {
			continue
		}
		d.Found = true
		d.Node = pgNodeOf(realm, ns, v.Snap.Status(realm, name))
		w := pgWorkOf(v.Snap, realm, name, v.Now)
		w.apply(&d.Node)
		if r, ok := w.runOf(short); ok && d.Node.Life() {
			d.Run = &r
		}
		nodes := []pgNode{d.Node}
		pgAttachSlurm(nodes, name, v.Snap.Slurm(realm, name))
		d.Node = nodes[0]
		d.Slurm = pgSlurmOf(v.Snap.Slurm(realm, name), nodes)
	}
	if !d.Found {
		return d
	}
	if c, ok := v.Snap.Cluster(realm, name); ok {
		d.ClusterPill = pgClusterOf(v.Snap, realm, c, v.Snap.Monitorings[realm], v.Now).Pill()
	}
	n := d.Node
	sind := pgSind(realm)
	d.Commands = []pgCmd{
		{"Log in to the node", fmt.Sprintf("%s ssh %s", sind, n.Arg)},
		{"Follow its journal", fmt.Sprintf("%s logs %s %s -f -n 200", sind, n.Arg, pgServiceOf(n.Role))},
	}
	if d.Run != nil {
		// A command creates or deletes the node: an action would get in
		// its way.
		return d
	}
	switch n.State {
	case cluster.StateRunning:
		d.Actions = []pgAction{n.power("Shut down…", "", "stop"), n.power("Reboot…", "", "reboot"), n.power("Freeze…", "", "pause")}
	case cluster.StatePaused:
		d.Actions = []pgAction{n.power("Unfreeze…", "", "unpause")}
	default:
		d.Actions = []pgAction{n.power("Start…", "primary", "start")}
	}
	if n.Worker() {
		d.Actions = append(d.Actions, n.power("Drain…", "", "drain"), n.power("Resume…", "", "resume"),
			pgDialogAction("Delete worker…", "danger", "delete-worker", "realm", realm, "cluster", name, "nodes", short))
	}
	return d
}

// LogsLink returns the address of the Logs tab with the node's log: the
// journal of its Slurm service while it runs, else its container output,
// which Docker keeps for a stopped container too.
func (d *pgNodeView) LogsLink() string {
	if !d.Node.Running() {
		return pgLogsURL(d.Realm, d.Cluster, d.Short, "")
	}
	return pgLogsURL(d.Realm, d.Cluster, d.Short, pgServiceOf(d.Node.Role))
}

// IssueLogsLink returns the address of the log that tells most about the
// node's issues: the journal of a service that is down, its Slurm service
// first, or else the node's log.
func (d *pgNodeView) IssueLogsLink() string {
	down := d.Node.Down()
	if len(down) == 0 {
		return d.LogsLink()
	}
	unit := down[0]
	if slurm := pgServiceOf(d.Node.Role); slices.Contains(down, slurm) {
		unit = slurm
	}
	return pgLogsURL(d.Realm, d.Cluster, d.Short, unit)
}

// ServiceLogsLink returns the address of the journal of one service of the
// node.
func (d *pgNodeView) ServiceLogsLink(unit string) string {
	return pgLogsURL(d.Realm, d.Cluster, d.Short, unit)
}
