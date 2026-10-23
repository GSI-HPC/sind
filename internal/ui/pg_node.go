// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"slices"

	"github.com/GSI-HPC/sind/internal/ui/store"
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
	watch := func(p Params) []store.Topic {
		realm, name := p.Get("realm"), p.Get("cluster")
		return []store.Topic{store.ClustersTopic(realm), store.NodesTopic(realm), store.StatusTopic(realm, name)}
	}
	load := func(v *View) error { v.Data = pgLoadNode(v); return nil }
	return []Fragment{
		{Name: "pg-node-head", Params: params, Watch: watch, Load: load},
		{Name: "pg-node-body", Params: params, Watch: watch, Load: load},
	}
}

// pgNodeView is what a node page shows.
type pgNodeView struct {
	// Found is false when the snapshot knows no such node.
	Found                 bool
	Realm, Cluster, Short string
	Node                  pgNode
	ClusterState          cluster.State
	Actions               []pgAction
	Commands              []pgCmd
}

// ClusterPillClass returns the class of the pill of the cluster's state.
func (d *pgNodeView) ClusterPillClass() string { return pgClusterClass(d.ClusterState) }

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
	}
	if !d.Found {
		return d
	}
	if c, ok := v.Snap.Cluster(realm, name); ok {
		d.ClusterState = c.State
	}
	n := d.Node
	power := func(label, class, op string) pgAction {
		return pgDialogAction(label, class, "power", "realm", realm, "cluster", name, "nodes", short, "op", op)
	}
	switch n.State {
	case cluster.StateRunning:
		d.Actions = []pgAction{power("Shut down…", "", "stop"), power("Reboot…", "", "reboot"), power("Freeze…", "", "pause")}
	case cluster.StatePaused:
		d.Actions = []pgAction{power("Unfreeze…", "", "unpause")}
	default:
		d.Actions = []pgAction{power("Start…", "primary", "start")}
	}
	if n.Worker() {
		d.Actions = append(d.Actions, power("Drain…", "", "drain"), power("Resume…", "", "resume"),
			pgDialogAction("Delete worker…", "danger", "delete-worker", "realm", realm, "cluster", name, "nodes", short))
	}
	sind := pgSind(realm)
	d.Commands = []pgCmd{
		{"Log in to the node", fmt.Sprintf("%s ssh %s", sind, n.Arg)},
		{"Follow its journal", fmt.Sprintf("%s logs %s %s -f -n 200", sind, n.Arg, pgServiceOf(n.Role))},
	}
	return d
}

// ClusterLink returns the address of the node's cluster page.
func (d *pgNodeView) ClusterLink() string { return pgClusterURL(d.Realm, d.Cluster, "") }

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
