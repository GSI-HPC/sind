// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
)

// The Logs tab of the cluster page keeps what it shows in its address, so
// that a reload or a shared link shows the same: ?node= the node,
// &source=journal for the journal and &unit= the unit (empty for every
// unit). logs.js writes them as they change.

// logsTabParams are the query keys of the Logs tab.
var logsTabParams = []string{"node", "source", "unit"}

// LogsTabView is what the Logs tab of the cluster page shows.
type LogsTabView struct {
	Realm, Cluster string
	// Nodes are the nodes of the cluster, the one shown selected.
	Nodes []logsNodeOption
	Node  string
	// Source is docker or journal.
	Source string
	// Units are the units of the node shown, after the entry for all.
	Units []logsChoice
	Unit  string
	// Sind is the command that the terminal line starts with, and Suffix
	// what follows a short name in a node argument.
	Sind, Suffix string
}

// logsNodeOption is a node of the node picker.
type logsNodeOption struct {
	Short string
	// Units are the node's units, separated by spaces.
	Units    string
	Label    string
	Selected bool
}

// logsChoice is an entry of a picker.
type logsChoice struct {
	Value, Label string
	Selected     bool
}

// logsUnits returns the systemd units whose journal the tab offers for a
// node of the role: its Slurm services first, then munge and sshd.
func logsUnits(r config.Role) []string {
	slurm := pgServiceOf(r)
	if r == config.RoleDB {
		return []string{slurm, "mariadb", "munge", "sshd"}
	}
	return []string{slurm, "munge", "sshd"}
}

// LogsTab returns what the Logs tab of the view's cluster shows, from the
// view's Params. A value that does not fit, such as a node that the
// cluster does not have, falls back to the default.
func (v *View) LogsTab() *LogsTabView {
	p := v.Params
	realm, name := p.Get("realm"), p.Get("cluster")
	d := &LogsTabView{Realm: realm, Cluster: name, Sind: pgSind(realm)}
	if name != config.DefaultClusterName {
		d.Suffix = "." + name
	}
	var units []string
	for _, n := range v.Snap.ClusterNodes(realm, name) {
		short := pgShort(realm, name, n.Container)
		o := logsNodeOption{Short: short, Units: strings.Join(logsUnits(n.Role), " "), Label: short}
		if n.State != cluster.StateRunning {
			o.Label += " (" + string(n.State) + ")"
		}
		d.Nodes = append(d.Nodes, o)
		if short == p.Get("node") || units == nil {
			d.Node, units = short, logsUnits(n.Role)
		}
	}
	for i := range d.Nodes {
		d.Nodes[i].Selected = d.Nodes[i].Short == d.Node
	}
	d.Source = string(logsDocker)
	if p.Get("source") == string(logsJournal) {
		d.Source = string(logsJournal)
	}
	// No unit named is the node's Slurm service; an empty one is all.
	d.Unit = p.Get("unit")
	if !p.Has("unit") || d.Unit != "" && !slices.Contains(units, d.Unit) {
		d.Unit = ""
		if len(units) > 0 {
			d.Unit = units[0]
		}
	}
	d.Units = []logsChoice{{Value: "", Label: "all units", Selected: d.Unit == ""}}
	for _, u := range units {
		d.Units = append(d.Units, logsChoice{Value: u, Label: u, Selected: u == d.Unit})
	}
	return d
}

// Command returns the command of the terminal that shows the same log.
func (d *LogsTabView) Command() string {
	return logsCommand(d.Sind, d.Node+d.Suffix, d.Source, d.Unit, logsTail)
}

// logsCommand returns the sind command that follows a log: the container
// output of the node arg, the journal of one unit, or the whole journal,
// which only a command on the node shows. logs.js writes the same.
func logsCommand(sind, arg, source, unit string, lines int) string {
	switch {
	case source != string(logsJournal):
		return fmt.Sprintf("%s logs %s -f -n %d", sind, arg, lines)
	case unit != "":
		return fmt.Sprintf("%s logs %s %s -f -n %d", sind, arg, unit, lines)
	}
	return fmt.Sprintf("%s ssh %s -- journalctl -f -n %d", sind, arg, lines)
}

// pgLogsURL returns the address of the Logs tab of a cluster that shows the
// log of one node: the journal of unit, or the output of its container
// for no unit.
func pgLogsURL(realm, name, short, unit string) string {
	q := url.Values{"tab": {"logs"}, "node": {short}}
	if unit != "" {
		q.Set("source", string(logsJournal))
		q.Set("unit", unit)
	}
	return "/clusters/" + url.PathEscape(realm) + "/" + url.PathEscape(name) + "?" + q.Encode()
}
