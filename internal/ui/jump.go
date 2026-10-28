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

	"github.com/GSI-HPC/sind/internal/help"
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
// (Step), or a command of the page (Cmd).
type jumpItem struct {
	Label string
	// Kind is the small text at the right: "cluster · sind", "word".
	Kind string
	Href string
	// Dialog marks a link to a dialog, which opens over the page.
	Dialog bool
	// Topic is set for help entries: the pane opens it.
	Topic string
	// Step is the action whose cluster or realm the box asks for.
	Step string
	// Cmd is a command that help.js runs: theme or keys.
	Cmd string
	// Text is more to match, not shown.
	Text string

	rank int
	// quiet keeps an entry out of the list of an empty query.
	quiet bool
}

// jumpData is what the block help-jump-list renders from.
type jumpData struct {
	Query string
	// HelpOnly is set when the query starts with ?.
	HelpOnly bool
	// Step is the action that the box asks a cluster or realm for, and
	// Ask the question; both are empty in the first step.
	Step, Ask string
	Items     []jumpItem
	// Total is how many entries match; Items holds the first jumpMax.
	Total int
	// More is how many clusters the list of an empty query leaves out.
	More int
}

// Say is what the box tells a screen reader about the list. In a step, it
// starts with the question, as the field does not say it.
func (d *jumpData) Say() string { return strings.TrimSpace(d.Ask + " " + d.say()) }

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
// the realm that the top bar keeps. A realm or a cluster that the snapshot
// does not have, such as one that was deleted since, is not part of it.
func jumpContextOf(r *http.Request, from string, sn store.Snapshot) jumpContext {
	var c jumpContext
	if u, err := url.Parse(from); err == nil {
		segs := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(segs) >= 3 && (segs[0] == "clusters" || segs[0] == "nodes") {
			c.Realm, c.Cluster = segs[1], segs[2]
		} else {
			c.Realm = u.Query().Get("realm")
		}
	}
	if c.Realm == "" {
		if ck, err := r.Cookie(realmCookie); err == nil {
			c.Realm = ck.Value
		}
	}
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
// step=ACTION it lists the clusters, or realms, that the action can act on.
// from is the address of the page, for the cluster to offer first.
func jumpHandle(s *Server, w http.ResponseWriter, r *http.Request) {
	v, err := s.View(r, "q", "step", "from")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	ctx := jumpContextOf(r, v.Params.Get("from"), v.Snap)
	if step := v.Params.Get("step"); step != "" {
		d, ok := jumpStep(help.Default(), v.Snap, step, ctx, v.Params.Get("q"))
		if !ok {
			s.Fail(w, r, fmt.Errorf("action %s asks for nothing: %w", step, ErrNotFound))
			return
		}
		v.Data = d
	} else {
		v.Data = jump(help.Default(), v.Snap, ctx, v.Params.Get("q"))
	}
	s.Write(w, r, http.StatusOK, "help-jump-list", v)
}

// jump returns the entries for q: the clusters, pages and actions, the
// nodes of every realm, the latest runs, and help. Of the entries that
// match every word of q, those whose label is q come first, then those
// whose label starts with it, then those whose label holds it, then the
// rest; within each, clusters and pages before actions, help, nodes, runs
// and words. An empty q lists the first clusters, the actions that are
// used most and the pages (see jumpFirst).
func jump(h *help.Help, sn store.Snapshot, ctx jumpContext, q string) *jumpData {
	q = strings.TrimSpace(q)
	rest, only := strings.CutPrefix(q, "?")
	d := &jumpData{Query: q, HelpOnly: only}
	var items []jumpItem
	if !only {
		items = jumpObjects(h, sn, ctx)
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
	jumpPick(d, items, rest)
	return d
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

// jumpPick puts the entries that match q into d, best first.
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
		if !jumpHasAll(hay, terms) {
			continue
		}
		label := strings.ToLower(strings.TrimSuffix(it.Label, "…"))
		score := 3
		switch {
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
// a wildcard, the actions, and the nodes and runs of the snapshot.
func jumpObjects(h *help.Help, sn store.Snapshot, ctx jumpContext) []jumpItem {
	var items []jumpItem
	clusters := 0
	for _, realm := range slices.Sorted(maps.Keys(sn.Clusters)) {
		for _, c := range sn.Clusters[realm] {
			clusters++
			items = append(items, jumpItem{Label: c.Name, Kind: "cluster · " + realm,
				Href: pgClusterURL(realm, c.Name, ""), Text: realm + "/" + c.Name, rank: jumpRankCluster})
		}
	}
	for _, p := range h.Pages() {
		if !strings.Contains(p.Route, "{") {
			items = append(items, jumpItem{Label: p.Title, Kind: "page", Href: p.Route, Text: p.Summary, rank: jumpRankPage})
		}
	}
	for _, a := range h.Actions() {
		if it, ok := jumpAction(a, sn, ctx, clusters); ok {
			items = append(items, it)
		}
	}
	for _, realm := range slices.Sorted(maps.Keys(sn.Nodes)) {
		for _, n := range sn.Nodes[realm] {
			short, _, _ := strings.Cut(n.FQDN, ".")
			items = append(items, jumpItem{Label: short + "." + n.Cluster, Kind: "node · " + n.Cluster + " · " + realm,
				Href: pgNodeURL(realm, n.Cluster, short), Text: realm + "/" + n.Cluster + "/" + short + " " + string(n.Role) + " " + string(n.State),
				rank: jumpRankNode, quiet: true})
		}
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
func jumpAction(a help.Action, sn store.Snapshot, ctx jumpContext, clusters int) (jumpItem, bool) {
	it := jumpItem{Label: a.Title, Kind: "action", Text: a.Summary + " " + a.Command, rank: jumpRankAction, quiet: !slices.Contains(jumpDefaults, a.ID)}
	realms := sn.Realms
	switch {
	case jumpActionPages[a.ID] != "":
		it.Href = jumpActionPages[a.ID]
	case actSpecs[a.ID].NeedsCluster && clusters == 0:
		return it, false
	case actSpecs[a.ID].NeedsCluster && clusters == 1:
		for _, realm := range slices.Sorted(maps.Keys(sn.Clusters)) {
			for _, c := range sn.Clusters[realm] {
				it.Kind, it.Href, it.Dialog = "action · "+c.Name, pgDialog(a.ID, "realm", realm, "cluster", c.Name), true
			}
		}
	case actSpecs[a.ID].NeedsCluster:
		it.Label, it.Kind, it.Step = a.Title+"…", "action · pick a cluster", a.ID
	case a.ID == "create-cluster":
		it.Href, it.Dialog = pgDialog(a.ID, "realm", ctx.Realm), true
	case len(realms) == 0:
		return it, false
	case ctx.Realm != "" || len(realms) == 1:
		realm := cmp.Or(ctx.Realm, realms[0].Name)
		it.Kind, it.Href, it.Dialog = "action · realm "+realm, pgDialog(a.ID, "realm", realm), true
	default:
		it.Label, it.Kind, it.Step = a.Title+"…", "action · pick a realm", a.ID
	}
	return it, true
}

// jumpDefaults are the actions that the list of an empty query shows.
var jumpDefaults = []string{"create-cluster", "create-worker", "power"}

// jumpAsks are the questions of the step of each action that asks for a
// cluster or a realm.
var jumpAsks = map[string]string{
	"create-worker":   "Add workers to which cluster?",
	"delete-cluster":  "Delete which cluster?",
	"delete-worker":   "Delete workers of which cluster?",
	"restart-service": "Restart a service of a node of which cluster?",
	"power":           "Power the nodes of which cluster?",
	"connect":         "Connect a container to which realm?",
	"disconnect":      "Disconnect a container from which realm?",
	"delete-realm":    "Delete which realm?",
}

// jumpStep returns the entries of the step of the action id: its clusters
// with the cluster of the page first, or its realms, that match q. It
// reports false for an action that asks for nothing.
func jumpStep(h *help.Help, sn store.Snapshot, id string, ctx jumpContext, q string) (*jumpData, bool) {
	a, ok := h.Action(id)
	ask, asks := jumpAsks[id]
	if !ok || !asks {
		return nil, false
	}
	d := &jumpData{Query: strings.TrimSpace(q), Step: id, Ask: ask}
	var items []jumpItem
	if actSpecs[a.ID].NeedsCluster {
		for _, realm := range slices.Sorted(maps.Keys(sn.Clusters)) {
			for _, c := range sn.Clusters[realm] {
				it := jumpItem{Label: c.Name, Kind: realm + " · " + pgPlural(c.Workers, "worker", "workers"),
					Href: pgDialog(a.ID, "realm", realm, "cluster", c.Name), Dialog: true, Text: realm, rank: 1}
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
