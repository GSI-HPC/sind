// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
)

// The Power dialog has two faces. Opened for an action and nodes, as from a
// node page or the selection of the Nodes tab, it is a short confirmation:
// the title names the action and the nodes, the nodes show their issues,
// and the picker waits behind Change nodes. Opened without them, as from
// the jump box, it is the picker: an action segment, quick picks, a node
// set field and a table of the nodes with their Slurm state.

// actNodeChoice is a node of the picker of the power and delete worker
// dialogs, with its Slurm state and its issues.
type actNodeChoice struct {
	Short, Role, State string
	On                 bool
	// Node is the node as the pages show it.
	Node pgNode
	// Offs says, by power op, why an op does not fit the node, as JSON
	// for the page script, which turns off the ops that fit no ticked
	// node.
	Offs string
}

// actNodeChoiceOf returns the node n as the picker shows it.
func actNodeChoiceOf(n pgNode, on bool) actNodeChoice {
	offs := map[string]string{}
	for _, op := range action.PowerOps {
		if why := pgOpOff(n, op.ID); why != "" {
			offs[op.ID] = why
		}
	}
	b, _ := json.Marshal(offs)
	return actNodeChoice{Short: n.Short, Role: string(n.Role), State: string(n.State), On: on, Node: n, Offs: string(b)}
}

// actPick is a quick pick of the Power dialog: the nodes it ticks, which
// may be none.
type actPick struct {
	Label string
	// Nodes are the short names, separated by commas.
	Nodes string
	N     int
	// Class colours the count.
	Class string
}

// Picks are the quick picks of the Power dialog: the nodes that need a
// look, the idle and the drained workers, as sinfo says, and all workers.
func (d *actDialog) Picks() []actPick {
	pick := func(label, class string, f func(pgNode) bool) actPick {
		var names []string
		for _, c := range d.Nodes {
			if f(c.Node) {
				names = append(names, c.Short)
			}
		}
		return actPick{Label: label, Nodes: strings.Join(names, ","), N: len(names), Class: class}
	}
	slurmState := func(n pgNode) string {
		if n.Slurm == nil || n.Life() {
			return ""
		}
		return n.Slurm.State
	}
	return []actPick{
		pick("Need a look", "bad", pgNode.Problem),
		pick("Idle", "", func(n pgNode) bool { return slurmState(n) == "idle" }),
		pick("Drained", "", func(n pgNode) bool { return strings.HasPrefix(slurmState(n), "drain") }),
		pick("All workers", "", pgNode.Worker),
	}
}

// Picked are the nodes that the dialog opened with, which a short
// confirmation names.
func (d *actDialog) Picked() []actNodeChoice {
	var out []actNodeChoice
	for _, c := range d.Nodes {
		if c.On {
			out = append(out, c)
		}
	}
	return out
}

// PickedSet names the picked nodes as one node set, as the CLI takes it.
func (d *actDialog) PickedSet() string {
	var shorts []string
	for _, c := range d.Picked() {
		shorts = append(shorts, c.Short)
	}
	return actNodeSet(shorts)
}

// actNodeSet folds short names into one node set, such as worker-[0-2].
func actNodeSet(shorts []string) string { return action.NodeSet(shorts) }

// actOpView is an op of the action segment, and whether it is the one
// picked.
type actOpView struct {
	action.PowerOp
	On bool
}

// actOpGroup is a group of the action segment: Slurm, Containers, and
// those that can lose data.
type actOpGroup struct {
	ID, Label string
	Ops       []actOpView
}

// OpSegs are the groups of the action segment of the Power dialog.
func (d *actDialog) OpSegs() []actOpGroup {
	var out []actOpGroup
	for _, g := range action.PowerGroups() {
		og := actOpGroup{ID: g.ID, Label: g.Label}
		for _, op := range g.Ops {
			og.Ops = append(og.Ops, actOpView{op, d.Form.Get("op") == op.ID})
		}
		out = append(out, og)
	}
	return out
}

// OtherOps names the ops beside the picked one that fit a picked node,
// for the closed section of a short confirmation: "Drain · Resume · …".
func (d *actDialog) OtherOps() string {
	var labels []string
	for _, op := range action.PowerOps {
		if op.ID != d.Form.Get("op") && d.fits(op.ID) {
			labels = append(labels, op.Label)
		}
	}
	return strings.Join(labels, " · ")
}

// fits reports whether the power op fits one of the picked nodes.
func (d *actDialog) fits(op string) bool {
	return slices.ContainsFunc(d.Picked(), func(c actNodeChoice) bool { return pgOpOff(c.Node, op) == "" })
}

// actPgNodes returns the nodes of the cluster as the pages show them,
// none for a cluster that sind ui does not see.
func actPgNodes(v *View, realm, clusterName string) []pgNode {
	summary, ok := v.Snap.Cluster(realm, clusterName)
	if !ok {
		return nil
	}
	return pgClusterOf(v.Snap, realm, summary, v.Snap.Monitorings[realm], v.Now).Nodes
}

// actOff is why a power op fits none of the ticked nodes: the names of
// the nodes and, by node, why it does not fit, as pgOpOff says it.
type actOff struct {
	Names, Whys []string
}

// Why says why the op fits none of the nodes, in a few words: "worker-0
// runs already", "each runs already" when all nodes have the same
// reason, each node's reason for up to three nodes, and "" for more
// nodes with reasons of their own.
func (o actOff) Why() string {
	same := !slices.ContainsFunc(o.Whys, func(w string) bool { return w != o.Whys[0] })
	switch {
	case len(o.Names) == 1:
		return actOffOne(o.Names[0], o.Whys[0])
	case same:
		if rest, ok := strings.CutPrefix(o.Whys[0], "It "); ok {
			return "each " + rest
		}
		return actLowerFirst(o.Whys[0])
	case len(o.Names) <= 3:
		parts := make([]string, len(o.Names))
		for i := range o.Names {
			parts[i] = actOffOne(o.Names[i], o.Whys[i])
		}
		return strings.Join(parts, "; ")
	}
	return ""
}

// Text says why the op fits none of the nodes, or, without a reason in a
// few words, that it fits none of them.
func (o actOff) Text() string {
	return cmp.Or(o.Why(), fmt.Sprintf("fits none of the %d ticked nodes", len(o.Names)))
}

// actOffOne says why an op does not fit the node name: "worker-0 runs
// already", "controller: only workers take jobs".
func actOffOne(name, why string) string {
	if rest, ok := strings.CutPrefix(why, "It "); ok {
		return name + " " + rest
	}
	return name + ": " + actLowerFirst(why)
}

// actLowerFirst writes the first letter of s in lower case.
func actLowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// actPowerOffs says, by power op, why it fits none of the ticked nodes of
// the form f, as the Power dialog shows it; an op that fits one of them
// is not in it, and nothing is while no node is ticked.
func actPowerOffs(v *View, f action.Form) map[string]actOff {
	names := action.NodeNames(f)
	var ticked []pgNode
	for _, n := range actPgNodes(v, action.Realm(f), f.Get("cluster")) {
		if slices.Contains(names, n.Short) {
			ticked = append(ticked, n)
		}
	}
	offs := map[string]actOff{}
	if len(ticked) == 0 {
		return offs
	}
	for _, op := range action.PowerOps {
		var o actOff
		for _, n := range ticked {
			why := pgOpOff(n, op.ID)
			if why == "" {
				o = actOff{}
				break
			}
			o.Names, o.Whys = append(o.Names, n.Short), append(o.Whys, why)
		}
		if o.Names != nil {
			offs[op.ID] = o
		}
	}
	return offs
}

// PowerOffs is the line below the action segment of the Power dialog:
// the actions beside the picked one that fit none of the ticked nodes,
// and why, as "Off for the ticked nodes: Start (each runs already) ·
// Unfreeze (each is not frozen)."; "" for none. The picked action that
// fits none is the error of the preview.
func (p actPreview) PowerOffs() string {
	offs := actPowerOffs(p.View, p.Form)
	var said []string
	for _, op := range action.PowerOps {
		if o, ok := offs[op.ID]; ok && op.ID != p.Form.Get("op") {
			said = append(said, op.Label+" ("+o.Text()+")")
		}
	}
	if len(said) == 0 {
		return ""
	}
	return "Off for the ticked nodes: " + strings.Join(said, " · ") + "."
}

// actPowerMisfit says why the power op of the form f fits none of its
// ticked nodes, as a stale link may ask for: "Start fits none of the ticked
// nodes: worker-0 runs already. …"; "" while it fits one.
func actPowerMisfit(v *View, f action.Form) string {
	op, _ := action.PowerOpOf(f.Get("op"))
	o, ok := actPowerOffs(v, f)[op.ID]
	if !ok {
		return ""
	}
	text := op.Label + " fits none of the ticked nodes"
	if why := o.Why(); why != "" {
		text += ": " + why
	}
	return text + ". Pick another action or other nodes."
}

// actPowerOK names the button of the Power dialog while the form still
// lacks a part, as the command would: the op and the ticked nodes,
// "Drain worker-0", "Reboot 3 workers", or "Reboot nodes" while none is
// ticked; "" without an op.
func actPowerOK(sn store.Snapshot, realm, clusterName string, f action.Form) string {
	op, ok := action.PowerOpOf(f.Get("op"))
	if !ok {
		return ""
	}
	switch names := action.NodeNames(f); len(names) {
	case 0:
		return op.Heading("nodes")
	case 1:
		return op.Heading(names[0])
	default:
		return op.Heading(fmt.Sprintf("%d %s", len(names), action.NodesNoun(actPicked(sn, realm, clusterName, names))))
	}
}

// actPicked returns the nodes of the cluster that names names.
func actPicked(sn store.Snapshot, realm, clusterName string, names []string) []cluster.NodeSummary {
	var picked []cluster.NodeSummary
	for _, n := range sn.ClusterNodes(realm, clusterName) {
		if slices.Contains(names, action.ShortName(n)) {
			picked = append(picked, n)
		}
	}
	return picked
}

// actHead is the title and the lead of a dialog that follow its form.
type actHead struct{ Title, Lead string }

// actPowerHead returns the title and the lead of the Power dialog for the
// form f: the action and the nodes, as "Reboot worker-4.dev" and what it
// does to one node, "Reboot 3 workers of dev" and what it does to several,
// or, without an action, what the dialog offers.
func actPowerHead(sn store.Snapshot, realm, clusterName string, f action.Form) actHead {
	op, ok := action.PowerOpOf(f.Get("op"))
	if !ok {
		return actHead{"Power and drain: nodes of " + clusterName,
			"Drain and Resume change what Slurm does with the nodes. The other actions change their containers, up to 8 at a time."}
	}
	names := action.NodeNames(f)
	picked := actPicked(sn, realm, clusterName, names)
	switch {
	case len(names) == 1 && len(picked) == 1:
		return actHead{op.Heading(pgArg(clusterName, names[0])), op.One}
	case len(picked) == len(names) && len(names) > 1:
		return actHead{op.Heading(fmt.Sprintf("%d %s of %s", len(names), action.NodesNoun(picked), clusterName)), op.Text}
	}
	return actHead{op.Heading("nodes of " + clusterName), op.Text}
}

// actDeleteWorkersHead returns the title of the Delete workers dialog for
// the form f: the worker, as "Delete worker-4.dev", or how many, as
// "Delete 3 workers of dev".
func actDeleteWorkersHead(clusterName string, f action.Form) actHead {
	lead := "Deletes the workers you pick, then tells slurmctld. This cannot be undone; add workers again to get new ones."
	switch names := action.NodeNames(f); len(names) {
	case 0:
		return actHead{"Delete workers of " + clusterName, lead}
	case 1:
		return actHead{"Delete " + pgArg(clusterName, names[0]), lead}
	default:
		return actHead{fmt.Sprintf("Delete %d workers of %s", len(names), clusterName), lead}
	}
}

// actDeleteClustersHead returns the title and the lead of a delete of the
// clusters of the form f, which names the realms that go with them, as
// "Delete 3 clusters and realm sind".
func actDeleteClustersHead(sn store.Snapshot, f action.Form) actHead {
	refs := action.ClusterRefs(f)
	if len(refs) == 0 {
		return actHead{"Delete clusters", "Deletes several clusters with one confirmation."}
	}
	title := "Delete " + pgPlural(len(refs), "cluster", "clusters")
	switch gone := action.GoneRealms(sn, refs); len(gone) {
	case 0:
		return actHead{title, "This cannot be undone."}
	case 1:
		return actHead{title + " and realm " + gone[0], fmt.Sprintf("These are the last clusters of realm %s, so the realm goes too. This cannot be undone.", gone[0])}
	default:
		return actHead{title + " and realms " + pgJoin(gone), "These are the last clusters of their realms, so the realms go too. This cannot be undone."}
	}
}

// Head is the title and the lead of a dialog whose title names what it
// acts on, Power and Delete workers, for the form of the preview, which
// the preview swaps in as the user changes the action or the nodes; nil
// for another dialog, whose title stays.
func (p actPreview) Head() *actHead {
	var h actHead
	switch p.ID {
	case "power":
		h = actPowerHead(p.Snap, action.Realm(p.Form), p.Form.Get("cluster"), p.Form)
	case "delete-worker":
		h = actDeleteWorkersHead(p.Form.Get("cluster"), p.Form)
	case "delete-clusters":
		h = actDeleteClustersHead(p.Snap, p.Form)
	default:
		return nil
	}
	return &h
}

// actPowerDefaults fills in the Power dialog: the op of the link in its
// own words, none when the link gives none, as the dialog picks no action
// for the user; and the short confirmation when the link names an action
// and nodes that all exist, of which the action fits one. Else the picker
// shows why it fits none.
func actPowerDefaults(d *actDialog) {
	op, ok := action.PowerOpOf(d.Form.Get("op"))
	if !ok {
		d.Form.Del("op")
		return
	}
	d.Form.Set("op", op.ID)
	names := action.NodeNames(d.Form)
	d.Short = len(names) > 0 && len(d.Picked()) == len(names) && d.fits(op.ID)
}
