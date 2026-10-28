// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"cmp"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/internal/help"
	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
)

// jumpMax is the most entries that the jump box lists for a query.
const jumpMax = 14

// jumpFirstClusters is the most clusters that the list of an empty query
// shows, so that the actions and the pages still fit below them.
const jumpFirstClusters = 5

// The ranks of the kinds of entries: of two entries whose labels match as
// well, the one of the lower rank comes first, so that clusters and pages
// come before the nodes of a cluster.
const (
	jumpRankCluster = iota
	jumpRankPage
	jumpRankAction
	jumpRankHelp
	jumpRankNode
	jumpRankRun
	jumpRankWord
)

// jumpItem is an entry of the jump box: a link, a help entry that opens in
// the pane (Topic), an action that asks for its cluster or realm next
// (Step), a command of the page (Cmd), or a command to copy (Copy).
type jumpItem struct {
	Label string
	// Kind is the small text at the right: "cluster · sind", "word". An
	// entry with a command shows that instead.
	Kind string
	Href string
	// Dialog marks a link to a dialog, which opens over the page.
	Dialog bool
	// Topic is set for help entries: the pane opens it.
	Topic string
	// Step is the action whose cluster or realm the box asks for, and Op
	// the power op that the step passes on to the Power dialog.
	Step, Op string
	// Cmd is a command that help.js runs: theme or keys.
	Cmd string
	// Copy is a command that the entry copies instead of opening a page.
	Copy string
	// Text is more to match, not shown.
	Text string
	// Sub is the line below the label, and Pills the issues of a node.
	Sub   string
	Pills []pgPill
	// CLI is the command that does the same in a terminal, at the right;
	// Tab copies it.
	CLI string
	// ID is the id of the option, and Mono sets its label in the type of
	// code, as in a step, which lists names.
	ID   string
	Mono bool
	// Off marks an action that does not fit what it acts on, which Sub
	// says why: the box shows it dimmed and does not pick it.
	Off bool

	rank int
	// act is the id of the action that the entry opens, if any.
	act string
	// quiet keeps an entry out of the list of an empty query.
	quiet bool
	// group is the head that the entry stands under, which its rank gives
	// when it is empty (groupName), and note what the head says at its
	// right. after is the group that the group of the actions of a node or
	// a cluster follows.
	group, note, after string
}

// jumpGroup is a group of entries under a head, such as Actions or Nodes.
type jumpGroup struct {
	ID, Label, Note string
	Items           []jumpItem
}

// jumpGroupNames are the heads of the entries of each rank.
var jumpGroupNames = map[int]string{
	jumpRankCluster: "Clusters", jumpRankPage: "Pages", jumpRankAction: jumpGroupActions,
	jumpRankHelp: "Help", jumpRankNode: "Nodes", jumpRankRun: "Runs", jumpRankWord: "Help",
}

// groupName returns the head of the entry.
func (it jumpItem) groupName() string { return cmp.Or(it.group, jumpGroupNames[it.rank]) }

// jumpData is what the block help-jump-list renders from.
type jumpData struct {
	Query string
	// HelpOnly is set when the query starts with ?.
	HelpOnly bool
	// Step is the action that the box asks a cluster or realm for, and
	// Ask the question; both are empty in the first step.
	Step, Ask string
	// Op is the power op of a step of the Power dialog.
	Op string
	// Miss is what a query names after its verb when that names no node
	// and no cluster.
	Miss  string
	Items []jumpItem
	// Total is how many entries match; Items holds the first jumpMax.
	Total int
	// More is how many clusters the list of an empty query leaves out.
	More int
}

// Groups returns the entries under their heads, in order, each with the id
// of its option. The entries of a step have no head.
func (d *jumpData) Groups() []jumpGroup {
	var out []jumpGroup
	for i, it := range d.Items {
		it.ID, it.Mono = "jump-o"+strconv.Itoa(i), d.Step != ""
		name := it.groupName()
		if d.Step != "" {
			name = ""
		}
		if len(out) == 0 || out[len(out)-1].Label != name {
			out = append(out, jumpGroup{ID: "jump-g" + strconv.Itoa(len(out)), Label: name, Note: it.note})
		}
		out[len(out)-1].Items = append(out[len(out)-1].Items, it)
	}
	return out
}

// HasCLI reports whether an entry shows a command, which Tab copies.
func (d *jumpData) HasCLI() bool {
	return slices.ContainsFunc(d.Items, func(it jumpItem) bool { return it.CLI != "" })
}

// Say is what the box tells a screen reader about the list. In a step, it
// starts with the question, as the field does not say it.
func (d *jumpData) Say() string {
	return strings.Join(slices.DeleteFunc([]string{d.Ask, d.MissText(), d.say()}, func(s string) bool { return s == "" }), " ")
}

// MissText says that the target of a verb names nothing, empty when it
// names something.
func (d *jumpData) MissText() string {
	if d.Miss == "" {
		return ""
	}
	return "No node or cluster matches " + d.Miss + "."
}

func (d *jumpData) say() string {
	switch {
	case d.Total == 0:
		return "Nothing matches."
	case d.Query == "" && d.Step == "":
		return strconv.Itoa(len(d.Items)) + " results. Type to find more."
	case d.Total > len(d.Items):
		return fmt.Sprintf("%d of %d results. Type more to narrow the list.", len(d.Items), d.Total)
	case d.Total == 1:
		return "1 result."
	}
	return strconv.Itoa(d.Total) + " results."
}

// Foot is the line below the list: how many entries it leaves out, or what
// typing finds.
func (d *jumpData) Foot() string {
	switch {
	case d.Query == "" && d.Step == "" && d.More == 1:
		return "Type to find 1 more cluster, and nodes, runs, words and how-tos."
	case d.Query == "" && d.Step == "" && d.More > 1:
		return fmt.Sprintf("Type to find %d more clusters, and nodes, runs, words and how-tos.", d.More)
	case d.Query == "" && d.Step == "":
		return "Type to find more: nodes, runs, words and how-tos."
	case d.Total > len(d.Items):
		return fmt.Sprintf("%d of %d. Type more to narrow the list.", len(d.Items), d.Total)
	}
	return ""
}

// jumpContext is where the jump box opened: the realm and cluster of the
// page, as far as it has them.
type jumpContext struct {
	Realm, Cluster string
}

// jumpContextOf reads the context from the address of the page (from) and
// the scope of the session. A realm or a cluster that the snapshot does not
// have, such as one that was deleted since, is not part of it.
func jumpContextOf(from, scope string, sn store.Snapshot) jumpContext {
	var c jumpContext
	if u, err := url.Parse(from); err == nil {
		segs := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(segs) >= 3 && (segs[0] == "clusters" || segs[0] == "nodes") {
			c.Realm, c.Cluster = segs[1], segs[2]
		} else {
			c.Realm = u.Query().Get("realm")
		}
	}
	c.Realm = cmp.Or(c.Realm, scope)
	if config.CheckName("realm", c.Realm) != nil || c.Cluster != "" && config.CheckName("cluster", c.Cluster) != nil {
		return jumpContext{}
	}
	if !slices.ContainsFunc(sn.Realms, func(r cluster.RealmSummary) bool { return r.Name == c.Realm }) {
		return jumpContext{}
	}
	if !slices.ContainsFunc(sn.Clusters[c.Realm], func(s cluster.Summary) bool { return s.Name == c.Cluster }) {
		c.Cluster = ""
	}
	return c
}

// jumpHandle serves GET /jump?q=: the entries of the jump box that match q,
// as the list of the box. A query that starts with ? finds help only. With
// step=ACTION it lists the clusters, or realms, that the action can act on,
// and op is the power op that a step of Power passes on. from is the
// address of the page, for the cluster to offer first.
func jumpHandle(s *Server, w http.ResponseWriter, r *http.Request) {
	v, err := s.View(r, "q", "step", "op", "from")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	ctx := jumpContextOf(v.Params.Get("from"), v.Scope, v.Snap)
	if step := v.Params.Get("step"); step != "" {
		d, ok := jumpStep(help.Default(), v.Snap, step, v.Params.Get("op"), ctx, v.Params.Get("q"))
		if !ok {
			s.Fail(w, r, fmt.Errorf("action %s asks for nothing: %w", step, ErrNotFound))
			return
		}
		v.Data = d
	} else {
		v.Data = jump(help.Default(), v.Snap, v.Now, ctx, v.Params.Get("q"))
	}
	s.Write(w, r, http.StatusOK, "help-jump-list", v)
}

// jump returns the entries for q: the clusters, pages and actions, the
// nodes of every realm, the latest runs, and help. A query that starts with
// a verb (jumpVerbOf) lists the verb for what the rest names first, in the
// group Actions, then the nodes and clusters it names, then help and runs
// that hold the verb. Else, of the entries that match every word of q,
// those whose label is q come first, then those whose label starts with it,
// then those whose label holds it, then the rest; within each, clusters and
// pages before actions, help, nodes, runs and words; the entries stand
// under the head of their kind, the group of the best entry first and runs
// last, and a query that names one node or cluster lists its actions below
// it (jumpNodeMore, jumpClusterMore). An empty q lists the first clusters,
// the actions that are used most and the pages (see jumpFirst).
func jump(h *help.Help, sn store.Snapshot, now time.Time, ctx jumpContext, q string) *jumpData {
	q = strings.TrimSpace(q)
	rest, only := strings.CutPrefix(q, "?")
	d := &jumpData{Query: q, HelpOnly: only}
	var items []jumpItem
	nodes := pgSnapNodes(sn, "", now)
	clusters := pgClustersOf(sn, "", now)
	if !only {
		items = jumpObjects(h, sn, nodes, clusters, ctx)
	}
	if q == "" {
		jumpFirst(d, items)
		return d
	}
	for _, it := range jumpHelp(h, only) {
		// Help alone lists all of help before the user types.
		it.quiet = it.quiet && !only
		items = append(items, it)
	}
	if vq, ok := jumpVerbOf(rest); ok && !only {
		first, miss := jumpVerbItems(nodes, clusters, ctx, vq)
		d.Miss = miss
		// The pages and the other actions that match the whole query
		// follow, as the verb may be a word of theirs too: "start" a
		// cluster, the "log" of runs. Then a few entries of help that
		// hold the verb, and the runs of the same command.
		var also, others []jumpItem
		for _, it := range items {
			switch {
			case it.rank == jumpRankPage || it.rank == jumpRankAction && !jumpVerbActs[it.act]:
				if it.rank == jumpRankAction {
					it.group = jumpGroupOtherActions
				}
				also = append(also, it)
			case it.rank == jumpRankHelp || it.rank == jumpRankWord || it.rank == jumpRankRun:
				others = append(others, it)
			}
		}
		more, pick := &jumpData{}, &jumpData{}
		jumpPick(more, also, rest)
		jumpPick(pick, others, vq.Word)
		helps := 0
		pick.Items = slices.DeleteFunc(pick.Items, func(it jumpItem) bool {
			if it.rank == jumpRankRun {
				return false
			}
			helps++
			return helps > jumpVerbHelp
		})
		d.Items = slices.Concat(first, more.Items, pick.Items)
		d.Total = len(d.Items)
		d.Items = d.Items[:min(len(d.Items), jumpMax)]
		return d
	}
	if n, c := jumpNamed(nodes, clusters, ctx, rest); !only && (n != nil || c != nil) {
		if n != nil {
			items = append(items, jumpNodeMore(*n, "")...)
		} else {
			items = append(items, jumpClusterMore(*c)...)
		}
	}
	jumpPick(d, items, rest)
	return d
}

// jumpVerbHelp is the most entries of help that a query with a verb lists.
const jumpVerbHelp = 3

// jumpGroupOtherActions is the group of the actions that a query with a
// verb finds by its words, after the entries of the verb.
const jumpGroupOtherActions = "Other actions"

// jumpVerbActs are the actions whose entries the verbs make themselves, so
// that a query with a verb does not list them again.
var jumpVerbActs = map[string]bool{
	"power": true, "restart-service": true, "create-worker": true,
	"delete-cluster": true, "delete-clusters": true, "delete-worker": true, "delete-realm": true,
}

// jumpFirst puts the list of an empty query into d: the first
// jumpFirstClusters clusters, the actions of jumpDefaults and the pages, in
// this order. The pages come last, as the rail lists them too; the list has
// no limit, so that every one of them is there.
func jumpFirst(d *jumpData, items []jumpItem) {
	var clusters, actions, pages []jumpItem
	for _, it := range items {
		switch {
		case it.quiet:
		case it.rank == jumpRankCluster:
			clusters = append(clusters, it)
		case it.rank == jumpRankAction:
			actions = append(actions, it)
		default:
			pages = append(pages, it)
		}
	}
	d.Total = len(clusters) + len(actions) + len(pages)
	d.More = max(0, len(clusters)-jumpFirstClusters)
	d.Items = slices.Concat(clusters[:len(clusters)-d.More], actions, pages)
}

// jumpPick puts the entries that match q into d, best first, by group:
// the group of the best entry first, runs last, and the actions of a node
// or a cluster ("More for") right after the group of that node or cluster.
// An entry of a "More for" group matches as the node or cluster does.
func jumpPick(d *jumpData, items []jumpItem, q string) {
	q = strings.ToLower(strings.TrimSpace(q))
	terms := strings.Fields(q)
	type scored struct {
		jumpItem
		score int
	}
	var found []scored
	for _, it := range items {
		if q == "" {
			if !it.quiet {
				found = append(found, scored{it, 0})
			}
			continue
		}
		hay := strings.ToLower(it.Label + " " + it.Kind + " " + it.Text)
		more := it.after != ""
		if !more && !jumpHasAll(hay, terms) {
			continue
		}
		label := strings.ToLower(strings.TrimSuffix(it.Label, "…"))
		score := 3
		switch {
		case more:
		case label == q:
			score = 0
		case strings.HasPrefix(label, q):
			score = 1
		case strings.Contains(label, q):
			score = 2
		}
		found = append(found, scored{it, score})
	}
	slices.SortStableFunc(found, func(a, b scored) int {
		return cmp.Or(a.score-b.score, a.rank-b.rank)
	})
	// The order of the groups: by their first entry, which is their best,
	// runs last; "More for" right after its node or cluster.
	order := map[string]int{}
	for i, f := range found {
		if _, ok := order[f.groupName()]; !ok {
			order[f.groupName()] = i
		}
	}
	key := func(f scored) (int, int) {
		g := f.groupName()
		switch {
		case g == jumpGroupNames[jumpRankRun]:
			return len(found) + 1, 0
		case f.after != "":
			if i, ok := order[f.after]; ok {
				return i, 1
			}
		}
		return order[g], 0
	}
	slices.SortStableFunc(found, func(a, b scored) int {
		ka, sa := key(a)
		kb, sb := key(b)
		return cmp.Or(ka-kb, sa-sb)
	})
	d.Total = len(found)
	for _, f := range found[:min(len(found), jumpMax)] {
		d.Items = append(d.Items, f.jumpItem)
	}
}

// jumpHasAll reports whether hay holds every term.
func jumpHasAll(hay string, terms []string) bool {
	for _, t := range terms {
		if !strings.Contains(hay, t) {
			return false
		}
	}
	return true
}

// jumpHelp lists the entries of help: the how-tos, the words, the overview
// pages of help and the commands of the page, and with only (a query that
// starts with ?) the actions too.
func jumpHelp(h *help.Help, only bool) []jumpItem {
	items := []jumpItem{
		{Label: "Help for this page", Kind: "help", Cmd: "help", Text: "? pane", rank: jumpRankHelp, quiet: true},
		{Label: "All help", Kind: "help", Topic: "index", Text: "how-tos how to", rank: jumpRankHelp, quiet: true},
		{Label: "Words", Kind: "help", Topic: "words", Text: "glossary terms slurm", rank: jumpRankHelp, quiet: true},
		{Label: "Keys", Kind: "help", Topic: "keys", Text: "keyboard shortcuts chords", rank: jumpRankHelp, quiet: true},
		{Label: "Single-key shortcuts on or off", Kind: "setting", Cmd: "keys", Text: "keyboard keys shortcuts speech", rank: jumpRankHelp, quiet: true},
		{Label: "Light or dark", Kind: "setting", Cmd: "theme", Text: "theme colour color mode", rank: jumpRankHelp, quiet: true},
	}
	for _, t := range h.Howtos() {
		items = append(items, jumpItem{Label: t.Title, Kind: "how to", Topic: helpTopic(help.KindHowto, t.ID),
			Text: t.Summary + " " + strings.Join(t.Steps, " ") + " " + strings.Join(t.Commands, " "), rank: jumpRankHelp, quiet: true})
	}
	for _, w := range h.Words() {
		items = append(items, jumpItem{Label: w.Title, Kind: "word", Topic: helpTopic(help.KindWord, w.ID),
			Text: w.ID + " " + w.Summary, rank: jumpRankWord, quiet: true})
	}
	if only {
		for _, a := range h.Actions() {
			items = append(items, jumpItem{Label: a.Title, Kind: "action", Topic: helpTopic(help.KindAction, a.ID),
				Text: a.Summary + " " + a.Command, rank: jumpRankWord})
		}
	}
	return items
}

// jumpActionPages are the actions that start on a page instead of their
// dialog: the dialog that deletes several clusters deletes those ticked on
// the Clusters page, and has nothing to show without them.
var jumpActionPages = map[string]string{"delete-clusters": "/clusters"}

// jumpObjects lists what the user can open: the clusters, the pages without
// a wildcard, the actions, and the nodes and runs of the snapshot. Actions
// and pages are found by the words of a newcomer too (jumpSynonyms).
func jumpObjects(h *help.Help, sn store.Snapshot, nodes []pgNode, clusters []pgCluster, ctx jumpContext) []jumpItem {
	var items []jumpItem
	for _, c := range clusters {
		items = append(items, jumpClusterItem(c))
	}
	for _, p := range h.Pages() {
		if !strings.Contains(p.Route, "{") {
			items = append(items, jumpItem{Label: p.Title, Kind: "page", Href: p.Route, Text: p.Summary + " " + jumpSynonyms[p.ID], rank: jumpRankPage})
		}
	}
	for _, a := range h.Actions() {
		if it, ok := jumpAction(a, sn, ctx, len(clusters)); ok {
			it.Text += " " + jumpSynonyms[a.ID]
			it.act = a.ID
			items = append(items, it)
		}
	}
	for _, n := range nodes {
		items = append(items, jumpNodeItem(n))
	}
	for i, r := range sn.Runs {
		if i == 8 {
			break
		}
		status := r.Status
		if status == "" {
			status = "running"
		}
		items = append(items, jumpItem{Label: r.Title, Kind: "run · " + status, Href: "/runs/" + url.PathEscape(r.ID), rank: jumpRankRun, quiet: true})
	}
	return items
}

// jumpAction returns the entry of action a: a link to its dialog when the
// box knows what it acts on, else a step that asks for the cluster or the
// realm. An action with nothing to act on, such as Power without a
// cluster, has no entry. Only the actions of jumpDefaults show without a
// query, so that one that cannot be undone never sits under the cursor.
// The entry says what it acts on, and shows the command of the action for
// that (jumpActionCLI). It matches by its title and its command, not by
// the long summary of help, whose words name other things too.
func jumpAction(a help.Action, sn store.Snapshot, ctx jumpContext, clusters int) (jumpItem, bool) {
	it := jumpItem{Label: a.Title, Kind: "action", Text: a.Command, rank: jumpRankAction, quiet: !slices.Contains(jumpDefaults, a.ID)}
	realms := sn.Realms
	realm, name := ctx.Realm, ""
	switch {
	case jumpActionPages[a.ID] != "":
		it.Href, it.Sub = jumpActionPages[a.ID], "Tick them on the Clusters page"
	case actSpecs[a.ID].NeedsCluster && clusters == 0:
		return it, false
	case actSpecs[a.ID].NeedsCluster && clusters == 1:
		for _, r := range slices.Sorted(maps.Keys(sn.Clusters)) {
			for _, c := range sn.Clusters[r] {
				realm, name = r, c.Name
				it.Kind, it.Href, it.Dialog = "action · "+c.Name, pgDialog(a.ID, "realm", r, "cluster", c.Name), true
			}
		}
		it.Sub = "On " + name + ", the only cluster"
	case actSpecs[a.ID].PickCluster && ctx.Cluster != "":
		name = ctx.Cluster
		it.Kind, it.Href, it.Dialog = "action · "+ctx.Cluster, pgDialog(a.ID, "realm", ctx.Realm, "cluster", ctx.Cluster), true
		it.Sub = "On " + name + ", the cluster of this page"
	case actSpecs[a.ID].PickCluster:
		// The dialog asks for the cluster itself.
		it.Kind, it.Href, it.Dialog = "action · pick a cluster", pgDialog(a.ID, "realm", ctx.Realm), true
		it.Sub = "The dialog asks for the cluster first"
	case actSpecs[a.ID].NeedsCluster:
		it.Label, it.Kind, it.Step = a.Title+"…", "action · pick a cluster", a.ID
		it.Sub = "Pick a cluster first"
	case a.ID == "create-cluster":
		it.Href = pgDialog(a.ID, "realm", ctx.Realm)
		it.Sub = "Fill in its config, then create it"
		if ctx.Realm != "" {
			it.Sub = "In realm " + ctx.Realm + ", or another that you type"
		}
	case len(realms) == 0:
		return it, false
	case ctx.Realm != "" || len(realms) == 1:
		realm = cmp.Or(ctx.Realm, realms[0].Name)
		it.Kind, it.Href, it.Dialog = "action · realm "+realm, pgDialog(a.ID, "realm", realm), true
		it.Sub = "In realm " + realm
	default:
		it.Label, it.Kind, it.Step = a.Title+"…", "action · pick a realm", a.ID
		it.Sub = "Pick a realm first"
	}
	it.CLI = jumpActionCLI(a, realm, name)
	return it, true
}

// jumpActionCLI returns the command of action a for the realm and the
// cluster that its entry acts on, "" for either when the user picks it
// next: "sind --realm ci create worker dev --count N".
func jumpActionCLI(a help.Action, realm, name string) string {
	cmd := a.Command
	if rest, ok := strings.CutPrefix(cmd, "sind --realm REALM"); ok && realm != "" {
		cmd = pgSind(realm) + rest
	} else if rest, ok := strings.CutPrefix(cmd, "sind "); ok && !strings.HasPrefix(rest, "--realm") {
		cmd = pgSind(realm) + " " + rest
	}
	if name != "" {
		for _, ph := range []string{" CLUSTER", " NAME"} {
			cmd = strings.Replace(cmd, ph, pgClusterArg(name), 1)
		}
	}
	return cmd
}

// jumpDefaults are the actions that the list of an empty query shows.
var jumpDefaults = []string{"create-cluster", "create-worker", "power"}

// jumpAsks are the questions of the step of each action that asks for a
// cluster or a realm.
var jumpAsks = map[string]string{
	"delete-cluster":  "Delete which cluster?",
	"delete-worker":   "Delete workers of which cluster?",
	"restart-service": "Restart a service of a node of which cluster?",
	"power":           "Power the nodes of which cluster?",
	"connect":         "Connect a container to which realm?",
	"disconnect":      "Disconnect a container from which realm?",
	"delete-realm":    "Delete which realm?",
}

// jumpStep returns the entries of the step of the action id: its clusters
// with the cluster of the page first, or its realms, that match q. A step
// of Power with a power op (op) asks for the nodes of that op, and its
// entries open the dialog with it. It reports false for an action that asks
// for nothing.
func jumpStep(h *help.Help, sn store.Snapshot, id, op string, ctx jumpContext, q string) (*jumpData, bool) {
	a, ok := h.Action(id)
	ask, asks := jumpAsks[id]
	if !ok || !asks {
		return nil, false
	}
	d := &jumpData{Query: strings.TrimSpace(q), Step: id, Ask: ask}
	var opKV []string
	if p, ok := action.PowerOpOf(op); ok && id == "power" {
		d.Op, d.Ask, opKV = p.ID, p.Label+" the nodes of which cluster?", []string{"op", p.ID}
	}
	var items []jumpItem
	if actSpecs[a.ID].NeedsCluster {
		for _, realm := range slices.Sorted(maps.Keys(sn.Clusters)) {
			for _, c := range sn.Clusters[realm] {
				it := jumpItem{Label: c.Name, Kind: realm + " · " + pgPlural(c.Workers, "worker", "workers"),
					Href: pgDialog(a.ID, append([]string{"realm", realm, "cluster", c.Name}, opKV...)...), Dialog: true, Text: realm, rank: 1}
				if realm == ctx.Realm && c.Name == ctx.Cluster {
					it.Kind, it.rank = it.Kind+" · this page", 0
				}
				items = append(items, it)
			}
		}
	} else {
		for _, r := range sn.Realms {
			it := jumpItem{Label: r.Name, Kind: "realm · " + pgPlural(r.Clusters, "cluster", "clusters"),
				Href: pgDialog(a.ID, "realm", r.Name), Dialog: true, rank: 1}
			if r.Name == ctx.Realm {
				it.Kind, it.rank = it.Kind+" · this page", 0
			}
			items = append(items, it)
		}
	}
	jumpPick(d, items, d.Query)
	return d, true
}

// Noun is what the step lists: cluster or realm.
func (d *jumpData) Noun() string {
	if actSpecs[d.Step].NeedsCluster {
		return "cluster"
	}
	return "realm"
}
