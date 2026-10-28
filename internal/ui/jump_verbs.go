// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/GSI-HPC/go-nodeset"
	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/pkg/probe"
)

// The jump box reads a query that starts with a verb as a command, as a
// terminal would: "reboot worker-4", "logs worker-4", "add dev", "shell
// dev". The verb is a word of sind's commands or one that a newcomer says
// for it (jumpVerbWords), and the rest names the nodes, as a node set, or
// the cluster. The entries that it makes open the dialog with the verb and
// the nodes filled in, or copy the command, and come first, in the group
// Actions. A query that names one node or one cluster, with no verb, lists
// the actions of that node or cluster below it (jumpMore).

// The verbs of the jump box that are not power ops (action.PowerOps).
const (
	jumpVerbLogs    = "logs"
	jumpVerbSSH     = "ssh"
	jumpVerbEnter   = "enter"
	jumpVerbDelete  = "delete"
	jumpVerbAdd     = "add"
	jumpVerbRestart = "restart"
)

// jumpVerbWords maps the words that a query may start with to their verb:
// a power op, or one of the verbs above. "restart" is both a reboot and a
// restart of a service.
var jumpVerbWords = map[string]string{
	"reboot": "reboot", "restart": jumpVerbRestart,
	"shutdown": "shutdown", "stop": "shutdown", "off": "shutdown", "halt": "shutdown",
	"start": "on", "on": "on", "boot": "on",
	"freeze": "freeze", "pause": "freeze", "unfreeze": "unfreeze", "unpause": "unfreeze",
	"drain": "drain", "resume": "resume", "undrain": "resume",
	"logs": jumpVerbLogs, "log": jumpVerbLogs, "journal": jumpVerbLogs, "journalctl": jumpVerbLogs,
	"ssh": jumpVerbSSH, "login": jumpVerbSSH,
	"enter": jumpVerbEnter, "shell": jumpVerbEnter, "terminal": jumpVerbEnter, "console": jumpVerbEnter,
	"delete": jumpVerbDelete, "remove": jumpVerbDelete, "rm": jumpVerbDelete, "destroy": jumpVerbDelete,
	"add": jumpVerbAdd, "grow": jumpVerbAdd, "scale": jumpVerbAdd,
}

// jumpVerbCommands are what sind's command line calls each verb that is
// not a power op, for the note of the group: "shell = sind enter".
var jumpVerbCommands = map[string]string{
	jumpVerbLogs: "sind logs", jumpVerbSSH: "sind ssh", jumpVerbEnter: "sind enter", jumpVerbDelete: "sind delete",
	jumpVerbAdd: "sind create worker", jumpVerbRestart: "sind power reboot",
}

// jumpUnits are the services that sind checks on its nodes, which a
// restart may name: "restart slurmd worker-4".
var jumpUnits = map[string]bool{
	string(probe.ServiceMunge): true, string(probe.ServiceSSHD): true, string(probe.ServiceSlurmctld): true,
	string(probe.ServiceSlurmd): true, string(probe.ServiceSlurmdbd): true, string(probe.ServiceMariadb): true,
	string(probe.ServiceSackd): true, string(probe.ServiceSlurmrestd): true,
}

// jumpFillers are the words between a verb and what it acts on that name
// nothing: "add workers to dev", "delete cluster dev".
var jumpFillers = map[string]bool{
	"a": true, "the": true, "to": true, "on": true, "of": true, "in": true, "for": true, "from": true,
	"node": true, "nodes": true, "worker": true, "workers": true, "cluster": true, "service": true,
}

// jumpVerbQuery is a query that starts with a verb.
type jumpVerbQuery struct {
	// Word is the verb as typed, Verb what it means.
	Word, Verb string
	// Words are the words after it, without the fillers, and Unit the
	// service that a restart names among them.
	Words []string
	Unit  string
}

// jumpVerbOf reads q as a verb and what follows it. A query may start as
// a command line does, with "sind", "sind power reboot", "sind create
// worker" or "sind delete worker".
func jumpVerbOf(q string) (jumpVerbQuery, bool) {
	words := strings.Fields(strings.ToLower(q))
	if len(words) > 1 && words[0] == "sind" {
		words = words[1:]
	}
	switch {
	case len(words) > 1 && words[0] == "power":
		words = words[1:]
	case len(words) > 1 && words[0] == "create" && strings.HasPrefix(words[1], "worker"):
		words[0] = "add"
	}
	if len(words) == 0 {
		return jumpVerbQuery{}, false
	}
	verb, ok := jumpVerbWords[words[0]]
	if !ok {
		return jumpVerbQuery{}, false
	}
	vq := jumpVerbQuery{Word: words[0], Verb: verb}
	for _, w := range words[1:] {
		switch {
		case jumpFillers[w]:
		case verb == jumpVerbRestart && jumpUnits[w] && vq.Unit == "":
			vq.Unit = w
		default:
			vq.Words = append(vq.Words, w)
		}
	}
	return vq, true
}

// op returns the power op of the verb, if it is one.
func (vq jumpVerbQuery) op() (action.PowerOp, bool) {
	if vq.Verb == jumpVerbRestart {
		return action.PowerOpOf("reboot")
	}
	return action.PowerOpOf(vq.Verb)
}

// Note says what the verb is in sind's commands, for the head of the
// group: "stop = sind power shutdown".
func (vq jumpVerbQuery) note() string {
	if vq.Unit != "" {
		return vq.Word + " " + vq.Unit + " = sind ssh NODE -- systemctl restart " + vq.Unit
	}
	if op, ok := vq.op(); ok && vq.Verb != jumpVerbRestart {
		if op.Slurm() {
			return vq.Word + " = sind exec -- scontrol update State=" + strings.ToUpper(op.ID)
		}
		return vq.Word + " = sind power " + op.ID
	}
	return vq.Word + " = " + jumpVerbCommands[vq.Verb]
}

// jumpTarget is what the words after a verb, or a query of its own, name:
// nodes, by their cluster, and clusters.
type jumpTarget struct {
	// Groups are the nodes it names, one entry for each cluster, the
	// cluster of the page first.
	Groups [][]pgNode
	// Clusters are the clusters it names.
	Clusters []pgCluster
}

// one returns the one node that the target names, if it names one.
func (t jumpTarget) one() (pgNode, bool) {
	if len(t.Groups) == 1 && len(t.Groups[0]) == 1 && len(t.Clusters) == 0 {
		return t.Groups[0][0], true
	}
	return pgNode{}, false
}

// jumpTargetOf returns what words name among the nodes and the clusters.
// The words are a node set, as sind's commands take it: worker-4.dev,
// worker-[1-3].dev; a name without a cluster names the node of that name
// in each cluster. A word that names nothing exactly names the nodes and
// clusters whose names start with it.
func jumpTargetOf(nodes []pgNode, clusters []pgCluster, ctx jumpContext, words []string) jumpTarget {
	var t jumpTarget
	if len(words) == 0 {
		return t
	}
	var picked []pgNode
	// names holds the part of the node set that names each name, as
	// typed: the groups and the clusters keep that order.
	names := map[string]int{}
	for i, part := range jumpSetParts(strings.Join(words, ",")) {
		if set, err := nodeset.Parse(part); err == nil {
			for _, name := range set.Expand() {
				if _, ok := names[name]; !ok {
					names[name] = i
				}
			}
		}
	}
	typed := map[string]int{}
	at := func(keys ...string) (int, bool) {
		best, ok := 0, false
		for _, k := range keys {
			if i, has := names[k]; has && (!ok || i < best) {
				best, ok = i, true
			}
		}
		return best, ok
	}
	for _, n := range nodes {
		if i, ok := at(n.Short+"."+n.Cluster, n.Arg, n.Short); ok {
			picked = append(picked, n)
			k := n.Realm + "/" + n.Cluster
			if j, seen := typed[k]; !seen || i < j {
				typed[k] = i
			}
		}
	}
	for _, c := range clusters {
		if i, ok := names[c.Name]; ok {
			t.Clusters = append(t.Clusters, c)
			typed["cluster "+c.Realm+"/"+c.Name] = i
		}
	}
	if len(picked) == 0 && len(t.Clusters) == 0 && len(words) == 1 {
		w := words[0]
		for _, n := range nodes {
			if strings.HasPrefix(n.Arg, w) || strings.HasPrefix(n.Short+"."+n.Cluster, w) {
				picked = append(picked, n)
			}
		}
		for _, c := range clusters {
			if strings.HasPrefix(c.Name, w) {
				t.Clusters = append(t.Clusters, c)
			}
		}
	}
	by := map[[2]string][]pgNode{}
	for _, n := range picked {
		k := [2]string{n.Realm, n.Cluster}
		by[k] = append(by[k], n)
	}
	keys := slices.SortedFunc(maps.Keys(by), func(a, b [2]string) int {
		here := func(k [2]string) int {
			if k[0] == ctx.Realm && k[1] == ctx.Cluster {
				return 0
			}
			return 1
		}
		return cmp.Or(here(a)-here(b), typed[a[0]+"/"+a[1]]-typed[b[0]+"/"+b[1]],
			jumpOther(a[0], ctx.Realm)-jumpOther(b[0], ctx.Realm), strings.Compare(a[1], b[1]), strings.Compare(a[0], b[0]))
	})
	for _, k := range keys {
		t.Groups = append(t.Groups, by[k])
	}
	slices.SortStableFunc(t.Clusters, func(a, b pgCluster) int {
		here := func(c pgCluster) int {
			if c.Realm == ctx.Realm && c.Name == ctx.Cluster {
				return 0
			}
			return 1
		}
		return cmp.Or(here(a)-here(b), typed["cluster "+a.Realm+"/"+a.Name]-typed["cluster "+b.Realm+"/"+b.Name],
			jumpOther(a.Realm, ctx.Realm)-jumpOther(b.Realm, ctx.Realm))
	})
	return t
}

// jumpSetParts splits a node set at its commas outside of brackets, into
// the parts as typed: "worker-[0-1].a,worker-2.b" is two parts.
func jumpSetParts(s string) []string {
	var parts []string
	depth, from := 0, 0
	for i, r := range s {
		switch {
		case r == '[':
			depth++
		case r == ']' && depth > 0:
			depth--
		case r == ',' && depth == 0:
			parts = append(parts, s[from:i])
			from = i + 1
		}
	}
	return append(parts, s[from:])
}

// jumpGroupActions is the group of the entries that act.
const jumpGroupActions = "Actions"

// jumpMaxTargets is the most clusters or groups of nodes that a verb makes
// entries for, and the most clusters that a verb without a target offers.
const jumpMaxTargets = 4

// jumpVerbItems returns the entries of a query that starts with a verb:
// the verb for each group of nodes and each cluster that the rest names,
// then the verb for others, which asks for the cluster; and the nodes and
// clusters it names, with the other actions of a node it names alone. miss
// is the rest when it names nothing, so that a typo does not pass for a
// target.
func jumpVerbItems(nodes []pgNode, clusters []pgCluster, ctx jumpContext, vq jumpVerbQuery) (items []jumpItem, miss string) {
	t := jumpTargetOf(nodes, clusters, ctx, vq.Words)
	if len(vq.Words) > 0 && len(t.Groups)+len(t.Clusters) == 0 {
		miss = strings.Join(vq.Words, " ")
	}
	var acts []jumpItem
	for _, g := range t.Groups[:min(len(t.Groups), jumpMaxTargets)] {
		acts = append(acts, jumpNodeVerb(vq, g)...)
	}
	for _, c := range t.Clusters[:min(len(t.Clusters), jumpMaxTargets)] {
		acts = append(acts, jumpClusterVerb(vq, c)...)
	}
	acts = append(acts, jumpAnyVerb(nodes, clusters, ctx, vq, len(t.Groups)+len(t.Clusters) > 0)...)
	note := vq.note()
	for i := range acts {
		acts[i].group, acts[i].note = jumpGroupActions, note
	}
	var objs []jumpItem
	for _, g := range t.Groups {
		for _, n := range g {
			objs = append(objs, jumpNodeItem(n))
		}
	}
	for _, c := range t.Clusters {
		objs = append(objs, jumpClusterItem(c))
	}
	if n, ok := t.one(); ok {
		// The first fix that an entry of the verb offers already is not
		// offered again.
		more := jumpNodeMore(n, vq.Verb)
		more = slices.DeleteFunc(more, func(m jumpItem) bool {
			return slices.ContainsFunc(acts, func(a jumpItem) bool { return a.Href != "" && a.Href == m.Href })
		})
		objs = append(objs, more...)
	}
	return append(acts, objs...), miss
}

// jumpSlurmCLI returns the command that drains or resumes nodes of a
// cluster in a terminal, as the Power dialog runs it; a drain needs a
// reason, which WHY stands for. NODES and CLUSTER stand for what the user
// picks next.
func jumpSlurmCLI(realm, cluster, set string, op action.PowerOp) string {
	cl := " CLUSTER"
	if cluster != "" {
		cl = pgClusterArg(cluster)
	}
	cli := pgSind(realm) + " exec" + cl + " -- scontrol update NodeName=" + set + " State=" + strings.ToUpper(op.ID)
	if op.ID == "drain" {
		cli += " Reason=WHY"
	}
	return cli
}

// jumpOffText says why a power op is off for node n, with its name: "It
// does not run" becomes "worker-1.legacy does not run.".
func jumpOffText(n pgNode, why string) string {
	if rest, ok := strings.CutPrefix(why, "It "); ok {
		why = n.Arg + " " + rest
	}
	return why + "."
}

// jumpNodeVerb returns the entries of the verb for nodes of one cluster.
func jumpNodeVerb(vq jumpVerbQuery, g []pgNode) []jumpItem {
	n := g[0]
	shorts := make([]string, len(g))
	for i, x := range g {
		shorts[i] = x.Short
	}
	set := action.NodeSet(shorts)
	arg := pgArg(n.Cluster, set)
	sind := pgSind(n.Realm)
	var out []jumpItem
	if op, ok := vq.op(); ok {
		it := jumpItem{Label: op.Heading(arg) + "…", Sub: "Opens Power with " + op.Label + " and " + set + " ticked",
			Href: pgDialog("power", "realm", n.Realm, "cluster", n.Cluster, "nodes", strings.Join(shorts, ","), "op", op.ID), Dialog: true,
			CLI: sind + " power " + op.ID + " " + arg, Kind: "action"}
		if op.Slurm() {
			it.CLI = jumpSlurmCLI(n.Realm, n.Cluster, set, op)
		}
		if len(g) == 1 {
			if why := pgOpOff(n, op.ID); why != "" {
				// The row is off; the first fix of the node, such as
				// Start for one that does not run, comes before it.
				it.Sub, it.Off = jumpOffText(n, why), true
				if l := pgLadder(n); len(l) > 0 {
					out = append(out, jumpItem{Label: l[0].Act.Name, Sub: "The first fix to try", Href: l[0].Act.URL, Dialog: true, CLI: l[0].Cmd, Kind: "action"})
				}
			}
		}
		out = append(out, it)
	}
	switch vq.Verb {
	case jumpVerbRestart:
		if len(g) == 1 && n.CanRestart() {
			// A service that the query names, or one that failed, is
			// what the user means: its restart comes before the reboot.
			unit := n.FailedUnit()
			if _, ok := n.Service(vq.Unit); ok {
				unit = vq.Unit
			}
			first := unit != ""
			unit = cmp.Or(unit, pgServiceOf(n.Role))
			a := n.RestartAction(unit, "")
			it := jumpItem{Label: a.Name, Sub: "Restarts one service; the container goes on", Href: a.URL, Dialog: true,
				CLI: sind + " ssh " + n.Arg + " -- systemctl restart " + unit, Kind: "action"}
			if first {
				out = append([]jumpItem{it}, out...)
			} else {
				out = append(out, it)
			}
		}
	case jumpVerbLogs:
		for _, x := range g {
			out = append(out, jumpLogsItem(x))
		}
	case jumpVerbSSH, jumpVerbEnter:
		for _, x := range g {
			out = append(out, jumpSSHItem(x))
		}
	case jumpVerbDelete:
		var workers []string
		for _, x := range g {
			if x.Worker() {
				workers = append(workers, x.Short)
			}
		}
		if len(workers) > 0 {
			ws := action.NodeSet(workers)
			sub := "Removes the worker containers. Jobs on them fail"
			if len(workers) == 1 {
				sub = "Removes the worker container. Jobs on it fail"
			}
			out = append(out, jumpItem{Label: "Delete " + pgArg(n.Cluster, ws) + "…", Sub: sub,
				Href: pgDialog("delete-worker", "realm", n.Realm, "cluster", n.Cluster, "nodes", strings.Join(workers, ",")), Dialog: true,
				CLI: sind + " delete worker " + pgArg(n.Cluster, ws), Kind: "action"})
		}
	case jumpVerbAdd:
		out = append(out, jumpClusterVerb(vq, pgCluster{Realm: n.Realm, Name: n.Cluster})...)
	}
	return out
}

// jumpClusterVerb returns the entries of the verb for a cluster.
func jumpClusterVerb(vq jumpVerbQuery, c pgCluster) []jumpItem {
	sind := pgSind(c.Realm)
	if op, ok := vq.op(); ok {
		out := []jumpItem{{Label: op.Label + " nodes of " + c.Name + "…", Sub: "Opens Power with " + op.Label + "; tick the nodes",
			Href: pgDialog("power", "realm", c.Realm, "cluster", c.Name, "op", op.ID), Dialog: true, CLI: sind + " power " + op.ID + " NODES", Kind: "action"}}
		if op.Slurm() {
			out[0].CLI = jumpSlurmCLI(c.Realm, c.Name, "NODES", op)
		}
		if vq.Verb == jumpVerbRestart {
			kv := []string{"realm", c.Realm, "cluster", c.Name}
			if vq.Unit != "" {
				kv = append(kv, "unit", vq.Unit)
			}
			it := jumpItem{Label: "Restart a service of a node of " + c.Name + "…", Sub: "Restarts one service; the container goes on",
				Href: pgDialog("restart-service", kv...), Dialog: true, Kind: "action"}
			if vq.Unit != "" {
				it.Label = "Restart " + vq.Unit + " on a node of " + c.Name + "…"
				return []jumpItem{it, out[0]}
			}
			out = append(out, it)
		}
		return out
	}
	switch vq.Verb {
	case jumpVerbAdd:
		return []jumpItem{jumpAddItem(c)}
	case jumpVerbDelete:
		return []jumpItem{jumpDeleteItem(c)}
	case jumpVerbSSH, jumpVerbEnter:
		return []jumpItem{jumpEnterItem(c)}
	}
	return []jumpItem{{Label: "Logs of " + c.Name, Sub: "The Logs tab of the cluster", Href: pgClusterURL(c.Realm, c.Name, "logs"), CLI: sind + " logs NODE", Kind: "cluster page"}}
}

// jumpAddItem, jumpDeleteItem and jumpEnterItem are the entries that add
// workers to a cluster, delete it, and copy the command of its shell.
func jumpAddItem(c pgCluster) jumpItem {
	return jumpItem{Label: "Add workers to " + c.Name + "…", Sub: "New workers take the sizes of its newest worker",
		Href: pgDialog("create-worker", "realm", c.Realm, "cluster", c.Name), Dialog: true, CLI: pgSind(c.Realm) + " create worker" + pgClusterArg(c.Name), Kind: "action"}
}

func jumpDeleteItem(c pgCluster) jumpItem {
	return jumpItem{Label: "Delete cluster " + c.Name + "…", Sub: "Asks first. This cannot be undone",
		Href: pgDialog("delete-cluster", "realm", c.Realm, "cluster", c.Name), Dialog: true, CLI: pgSind(c.Realm) + " delete cluster" + pgClusterArg(c.Name), Kind: "action"}
}

func jumpEnterItem(c pgCluster) jumpItem {
	cmd := pgSind(c.Realm) + " enter" + pgClusterArg(c.Name)
	return jumpItem{Label: "Copy the shell command of " + c.Name, Sub: "Opens a shell on its submitter or controller", Copy: cmd, CLI: cmd, Kind: "copy"}
}

// jumpLogsItem and jumpSSHItem are the entries that open the log of a node
// and copy the command of its shell.
func jumpLogsItem(n pgNode) jumpItem {
	cli := pgSind(n.Realm) + " logs " + n.Arg
	if u := n.LogUnit(); u != "" {
		cli += " " + u
	}
	return jumpItem{Label: n.LogsName(), Sub: "The Logs tab of the node", Href: n.LogsLink(), CLI: cli, Kind: "node page"}
}

func jumpSSHItem(n pgNode) jumpItem {
	return jumpItem{Label: "Copy the ssh command of " + n.Arg, Sub: "Logs in to the node", Copy: n.SSH(), CLI: n.SSH(), Kind: "copy"}
}

// jumpAnyVerb returns the entries of the verb for a cluster that the user
// picks next, or for the cluster of the page; others says that entries for
// what the query names come before them. A verb with nothing to pick, such
// as logs, offers what fits it: the logs of the nodes that need a look, a
// shell on each cluster.
func jumpAnyVerb(nodes []pgNode, clusters []pgCluster, ctx jumpContext, vq jumpVerbQuery, others bool) []jumpItem {
	here := func() (pgCluster, bool) {
		i := slices.IndexFunc(clusters, func(c pgCluster) bool { return c.Realm == ctx.Realm && c.Name == ctx.Cluster })
		if i < 0 {
			return pgCluster{}, false
		}
		return clusters[i], true
	}
	if len(clusters) == 0 {
		return nil
	}
	// The commands of what the user picks next name the realm of the
	// scope, as the step lists its clusters first.
	sind := pgSind(ctx.Realm)
	if op, ok := vq.op(); ok {
		var out []jumpItem
		if c, ok := here(); ok && !others {
			out = jumpClusterVerb(vq, c)
		}
		label := op.Label + " nodes…"
		if others {
			label = op.Label + " other nodes…"
		}
		out = append(out, jumpItem{Label: label, Sub: "Pick a cluster, then tick the nodes", Step: "power", Op: op.ID,
			CLI: sind + " power " + op.ID + " NODES", Kind: "action"})
		if op.Slurm() {
			out[len(out)-1].CLI = jumpSlurmCLI(ctx.Realm, "", "NODES", op)
		}
		if vq.Verb == jumpVerbRestart {
			out = append(out, jumpItem{Label: "Restart a service…", Sub: "Pick a cluster, then the node and the service", Step: "restart-service",
				CLI: sind + " ssh NODE -- systemctl restart " + cmp.Or(vq.Unit, "UNIT"), Kind: "action"})
		}
		return out
	}
	if others {
		return nil
	}
	switch vq.Verb {
	case jumpVerbAdd:
		if c, ok := here(); ok {
			return []jumpItem{jumpAddItem(c)}
		}
		return []jumpItem{{Label: "Add workers…", Sub: "Pick the cluster first", Href: pgDialog("create-worker", "realm", ctx.Realm), Dialog: true, CLI: sind + " create worker CLUSTER", Kind: "action"}}
	case jumpVerbDelete:
		return []jumpItem{{Label: "Delete a cluster…", Sub: "Pick the cluster, then confirm", Step: "delete-cluster", CLI: sind + " delete cluster CLUSTER", Kind: "action"},
			{Label: "Delete workers…", Sub: "Pick the cluster, then tick the workers", Step: "delete-worker", CLI: sind + " delete worker NODES", Kind: "action"},
			{Label: "Delete several clusters", Sub: "Tick them on the Clusters page", Href: "/clusters", Kind: "action"},
			{Label: "Delete a realm…", Sub: "Pick the realm, then confirm", Step: "delete-realm", CLI: "sind --realm REALM delete realm", Kind: "action"}}
	case jumpVerbLogs:
		var out []jumpItem
		if c, ok := here(); ok {
			out = append(out, jumpClusterVerb(vq, c)...)
		}
		for _, n := range nodes {
			if n.Problem() && len(out) < jumpMaxTargets {
				out = append(out, jumpLogsItem(n))
			}
		}
		return out
	}
	// A shell: on the cluster of the page, else on each, those of the
	// realm of the scope first.
	if c, ok := here(); ok {
		return jumpClusterVerb(vq, c)
	}
	cs := slices.Clone(clusters)
	slices.SortStableFunc(cs, func(a, b pgCluster) int {
		return cmp.Compare(jumpOther(a.Realm, ctx.Realm), jumpOther(b.Realm, ctx.Realm))
	})
	var out []jumpItem
	for _, c := range cs[:min(len(cs), jumpMaxTargets)] {
		out = append(out, jumpClusterVerb(vq, c)...)
	}
	return out
}

// jumpOther returns 0 for the realm of the scope, else 1, to order by.
func jumpOther(realm, scope string) int {
	if realm == scope {
		return 0
	}
	return 1
}

// jumpNodeItem is the entry of a node: its page, with its issues.
func jumpNodeItem(n pgNode) jumpItem {
	sub := string(n.State)
	if r := n.SlurmReason(); r != "" {
		sub += " · " + r
	}
	return jumpItem{Label: n.Arg, Kind: "node · " + n.Cluster + " · " + n.Realm, Href: n.Link(), Sub: sub, Pills: n.Issues(),
		Text: n.Realm + "/" + n.Cluster + "/" + n.Short + " " + string(n.Role) + " " + string(n.State), rank: jumpRankNode, quiet: true}
}

// jumpClusterItem is the entry of a cluster: its page.
func jumpClusterItem(c pgCluster) jumpItem {
	return jumpItem{Label: c.Name, Kind: "cluster · " + c.Realm, Href: pgClusterURL(c.Realm, c.Name, ""), Text: c.Realm + "/" + c.Name, rank: jumpRankCluster}
}

// jumpNodeMore returns the actions of a node that a query names alone,
// for the group "More for" the node: its first fix, its log, its shell,
// and a reboot, but the entries of verb, which the query offers already.
func jumpNodeMore(n pgNode, verb string) []jumpItem {
	var out []jumpItem
	if l := pgLadder(n); len(l) > 0 {
		a := l[0].Act
		out = append(out, jumpItem{Label: a.Name, Sub: "The first fix to try", Href: a.URL, Dialog: true, CLI: l[0].Cmd, Kind: "action"})
	}
	if verb != jumpVerbLogs {
		out = append(out, jumpLogsItem(n))
	}
	if verb != jumpVerbSSH && verb != jumpVerbEnter {
		out = append(out, jumpSSHItem(n))
	}
	if op, _ := action.PowerOpOf("reboot"); verb != "reboot" && verb != jumpVerbRestart && n.Running() && !n.Life() {
		out = append(out, jumpItem{Label: op.Heading(n.Arg) + "…", Sub: op.One, Href: n.power("", "", "reboot").URL, Dialog: true,
			CLI: pgSind(n.Realm) + " power reboot " + n.Arg, Kind: "action"})
	}
	for i := range out {
		out[i].group, out[i].after = "More for "+n.Arg, jumpGroupNames[jumpRankNode]
	}
	return out
}

// jumpClusterMore returns the actions of a cluster that a query names
// alone, for the group "More for" the cluster: Add workers, its shell and
// Delete cluster, or none while a command creates or deletes it.
func jumpClusterMore(c pgCluster) []jumpItem {
	if c.Life != nil {
		return nil
	}
	out := []jumpItem{jumpAddItem(c), jumpEnterItem(c), jumpDeleteItem(c)}
	for i := range out {
		out[i].group, out[i].after = "More for "+c.Name, jumpGroupNames[jumpRankCluster]
	}
	return out
}

// jumpNamed returns the node or the cluster that q names alone, by its
// name as sind's commands take it, or by its short name when one node of
// the cluster of the page, or of all, has it.
func jumpNamed(nodes []pgNode, clusters []pgCluster, ctx jumpContext, q string) (*pgNode, *pgCluster) {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" || strings.ContainsAny(q, " ,[") {
		return nil, nil
	}
	var cs []pgCluster
	for _, c := range clusters {
		if c.Name == q {
			cs = append(cs, c)
		}
	}
	// A short name names the node of the cluster of the page first.
	var ns []pgNode
	for _, n := range nodes {
		if n.Short == q && n.Realm == ctx.Realm && n.Cluster == ctx.Cluster {
			ns = append(ns, n)
		}
	}
	if len(ns) == 0 {
		for _, n := range nodes {
			if n.Arg == q || n.Short+"."+n.Cluster == q {
				ns = append(ns, n)
			}
		}
	}
	if len(ns) == 0 && ctx.Cluster == "" {
		for _, n := range nodes {
			if n.Short == q {
				ns = append(ns, n)
			}
		}
	}
	switch {
	case len(ns) == 1 && len(cs) == 0:
		return &ns[0], nil
	case len(cs) == 1 && len(ns) == 0:
		return nil, &cs[0]
	}
	return nil, nil
}

// jumpSynonyms are more words that find an action or a page: the words of
// a newcomer, and of other tools, for what sind calls otherwise.
var jumpSynonyms = map[string]string{
	"create-cluster":  "make build new start a cluster",
	"create-worker":   "add grow scale more nodes workers",
	"delete-cluster":  "remove destroy tear down rm",
	"delete-clusters": "remove destroy tear down rm several",
	"delete-worker":   "remove shrink rm nodes",
	"power":           "reboot restart shutdown stop start boot turn off on freeze pause cut drain resume scontrol",
	"restart-service": "restart service systemctl daemon",
	"connect":         "attach join network link container",
	"disconnect":      "detach unlink network container",
	"delete-realm":    "remove destroy",
	"mesh":            "network hosts addresses ip resolve",
	"topology":        "map diagram network",
	"monitoring":      "grafana metrics prometheus dashboards loki",
	"doctor":          "check health host problems fix",
	"activity":        "history log runs events past",
	"nodes":           "machines hosts containers",
	"clusters":        "list all",
}
