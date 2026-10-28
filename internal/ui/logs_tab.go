// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
)

// The Logs tab of the cluster page keeps what it shows in its address, so
// that a reload or a shared link shows the same: ?node= the node,
// &source=journal for the journal, &unit= the unit (empty for every
// unit), &lines= the line count, &q= the filter and &level=warn for
// errors and warnings only. logs.js writes them as they change.

// logsTabParams are the query keys of the Logs tab.
var logsTabParams = []string{"node", "source", "unit", "lines", "q", "level"}

// logsLineCounts are the line counts that a stream may start with; the
// first is the default.
var logsLineCounts = []int{200, 1000, 5000}

// logsKeep is how many lines the tab keeps.
const logsKeep = 10000

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
	// Lines are the line counts to start with.
	Lines []logsChoice
	// Count is the line count that the stream starts with.
	Count int
	// Query is the filter, and Errors keeps errors and warnings only.
	// QueryMax is the longest filter, the longest value of a parameter.
	Query    string
	QueryMax int
	Errors   bool
	// Sind is the command that the terminal line starts with, and Suffix
	// what follows a short name in a node argument.
	Sind, Suffix string
	// Keep is how many lines the tab keeps.
	Keep int
	// Fixed is set on the Logs tab of a node page, whose node is that of
	// the page: the tab has no picker of the node. Ended is what the tab
	// says in place of Follow once the log of a node that does not run
	// ended, empty for one that runs.
	Fixed bool
	Ended string
}

// NodeLogsTab returns what the Logs tab of the view's node page shows:
// LogsTab, with the node of the page.
func (v *View) NodeLogsTab() *LogsTabView {
	d := v.LogsTab()
	d.Fixed = true
	if nv, ok := v.Data.(*pgNodeView); ok {
		d.Ended = nv.LogsEnded()
	}
	return d
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
	d := &LogsTabView{Realm: realm, Cluster: name, Sind: pgSind(realm), Keep: logsKeep, Query: p.Get("q"), QueryMax: maxParamLen, Errors: p.Get("level") == "warn"}
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
	d.Count = logsLineCounts[0]
	if n, err := strconv.Atoi(p.Get("lines")); err == nil && slices.Contains(logsLineCounts, n) {
		d.Count = n
	}
	for _, n := range logsLineCounts {
		d.Lines = append(d.Lines, logsChoice{Value: strconv.Itoa(n), Label: logsThousands(n) + " lines", Selected: n == d.Count})
	}
	return d
}

// Command returns the command of the terminal that shows the same log:
// the container output of the node, the journal of one unit, or the whole
// journal, which only a command on the node shows. logs.js writes the same.
func (d *LogsTabView) Command() string {
	arg := d.Node + d.Suffix
	switch {
	case d.Source != string(logsJournal):
		return fmt.Sprintf("%s logs %s -f -n %d", d.Sind, arg, d.Count)
	case d.Unit != "":
		return fmt.Sprintf("%s logs %s %s -f -n %d", d.Sind, arg, d.Unit, d.Count)
	}
	return fmt.Sprintf("%s ssh %s -- journalctl -f -n %d", d.Sind, arg, d.Count)
}

// logsThousands writes n with a comma between the thousands: 5,000.
func logsThousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// KeepText is Keep for a sentence: 10,000.
func (d *LogsTabView) KeepText() string {
	return logsThousands(d.Keep)
}
