// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"cmp"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/activity"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

// runListMax is the most runs the Activity page lists.
const runListMax = 500

// runFilters are the filters of the Activity page, in the order of its tabs.
var runFilters = []struct{ Key, Label string }{
	{"", "All"}, {"running", "Running"}, {"ok", "OK"}, {"failed", "Failed"}, {"canceled", "Stopped"},
}

// registerRunActivity adds the Activity page and its list of runs.
func registerRunActivity(r *Registry) {
	r.AddFragment(Fragment{
		Name:   "run-list",
		Topics: []store.Topic{store.TopicRuns, store.TopicTasks},
		Params: []string{"status", "q", "realm", "cluster"},
		Load: func(v *View) error {
			list, err := runListOf(v)
			v.Data = list
			return err
		},
	})
	// The realm picker stays on the page, and a realm it picks is the
	// one the lists show next.
	r.AddRoute(pgRealmRoute(Page{
		Name: "run-activity", Pattern: "GET /activity", Title: "Activity", Nav: "activity",
		Fragments: []string{"run-list"},
	}, false, true))
	r.SetNavCount("activity", runNavCount)
}

// runNavCount is the badge behind the Activity entry of the rail: the runs
// that go on, or else the runs that failed lately and that "Needs you"
// lists (pgFailures). A log without either shows none.
func runNavCount(snap store.Snapshot, now time.Time) NavCount {
	running := pgCount(snap.Runs, func(r store.Run) bool { return !r.Done() })
	failed := pgCount(pgFailures(snap, now), func(f pgFailure) bool { return f.Fixed == nil })
	switch {
	case running > 0:
		return NavCount{Text: strconv.Itoa(running) + " running", Class: "run", Label: pgPlural(running, "command runs", "commands run") + " now"}
	case failed > 0:
		return NavCount{Text: strconv.Itoa(failed) + " failed", Class: "bad", Label: pgPlural(failed, "run", "runs") + " failed in the last 6 hours"}
	}
	return NavCount{}
}

// runTab is a filter tab with the number of runs it holds, or a chip of a
// realm or a cluster, which Href turns on or off.
type runTab struct {
	Label string
	Href  string
	N     int
	On    bool
	// Tip says more about a chip, such as the realm of a cluster.
	Tip string
}

// runListRow is a run in the list.
type runListRow struct {
	ID        string
	Title     string
	Pill      string
	PillClass string
	// Chip says where a run came from that sind ui did not start.
	Chip string
	// Realm is the realm of the run, when sind ui knows it.
	Realm   string
	Started string
	Elapsed string
	Since   int64
	// Result says what came of the run (runResult), in the class
	// ResultClass.
	Result, ResultClass string
}

// runWord is a word of a run's title; Flag marks a flag with its value,
// such as "--count 3", which the page keeps on one line.
type runWord struct {
	Text string
	Flag bool
}

// Words splits the title into its words, for the page to keep each flag
// whole where the column wraps, with the value that follows it: a browser
// may break a line after a hyphen, or between a flag and its value.
func (r runListRow) Words() []runWord {
	var out []runWord
	for _, w := range strings.Fields(r.Title) {
		flag := strings.HasPrefix(w, "-")
		if n := len(out); n > 0 && !flag && out[n-1].Flag && !strings.Contains(out[n-1].Text, " ") && !strings.Contains(out[n-1].Text, "=") {
			out[n-1].Text += " " + w
			continue
		}
		out = append(out, runWord{Text: w, Flag: flag})
	}
	return out
}

// runList is the Activity page's list.
type runList struct {
	Tabs []runTab
	// Realms and Clusters are the chips that filter by realm and cluster;
	// Realms shows only with more than one realm.
	Realms   []runTab
	Clusters []runTab
	// Clear drops the filters of text, realm and cluster; empty without one.
	Clear string
	Rows  []runListRow
	// Total is the number of runs in the log, whatever the filter.
	Total int
	// More counts the runs that matched and did not fit.
	More int
}

// runMatches reports whether a run passes the filter.
func runMatches(run store.Run, filter string) bool {
	switch filter {
	case "":
		return true
	case "running":
		return !run.Done()
	}
	return run.Done() && run.Status == filter
}

// runQuery is what the Activity page filters by: the status of its tabs,
// a text, a realm and a cluster.
type runQuery struct{ Status, Text, Realm, Cluster string }

// href is the address of the Activity page with the query.
func (q runQuery) href() string {
	vals := url.Values{}
	for _, kv := range [][2]string{{"status", q.Status}, {"q", q.Text}, {"realm", q.Realm}, {"cluster", q.Cluster}} {
		if kv[1] != "" {
			vals.Set(kv[0], kv[1])
		}
	}
	if len(vals) == 0 {
		return "/activity"
	}
	return "/activity?" + vals.Encode()
}

// runEntry is a run with what the filters of the Activity page look at.
type runEntry struct {
	run   store.Run
	title string
	// realms are the realms the run may have worked on: that of its task,
	// or else each realm that has a cluster of its name. realm is the one
	// realm, when there is one.
	realms  []string
	realm   string
	cluster string
	// hay is what the text filter searches, in lower case.
	hay string
}

// runEntryOf finds the title, the realm and the cluster of a run: from its
// task, with the flags of its command line, or else from the target of the
// command, such as the cluster of "create cluster dev".
func runEntryOf(snap store.Snapshot, run store.Run) runEntry {
	e := runEntry{run: run, title: run.Title, cluster: run.Root.Node}
	display := ""
	if t, ok := runTaskOf(snap, run.Trace); ok {
		e.title, display = taskTitle(t, &run), t.Display
		e.cluster = cmp.Or(t.Cluster, e.cluster)
		if t.Realm != "" {
			e.realms = []string{t.Realm}
		}
	}
	if e.realms == nil && e.cluster != "" && !runOutlived(run) {
		for _, realm := range slices.Sorted(maps.Keys(snap.Clusters)) {
			if _, ok := snap.Cluster(realm, e.cluster); ok {
				e.realms = append(e.realms, realm)
			}
		}
	}
	if len(e.realms) == 1 {
		e.realm = e.realms[0]
	}
	e.title = cmp.Or(e.title, run.ID)
	// The names of the nodes find a run whose title folds them, as
	// worker-[0-2].dev.
	e.hay = strings.ToLower(strings.Join(append([]string{e.title, display, run.ID, e.realm}, activity.NodeNames(run.Root)...), "\n"))
	return e
}

// runOutlived reports whether the cluster of a run is gone since the run
// ended: a create cluster that did not end ok made none to keep, and a
// delete cluster that did removed it. A cluster of the name that exists now
// is another one, whose realm says nothing about the run's, which the log
// does not keep.
func runOutlived(run store.Run) bool {
	switch {
	case !run.Done():
		return false
	case run.Root.Name == "create cluster":
		return run.Status != store.StatusOK
	case run.Root.Name == "delete cluster":
		return run.Status == store.StatusOK
	}
	return false
}

// matches reports whether the entry passes the query, but for its status.
func (e runEntry) matches(q runQuery) bool {
	switch {
	case q.Realm != "" && !slices.Contains(e.realms, q.Realm):
		return false
	case q.Cluster != "" && e.cluster != q.Cluster:
		return false
	}
	for _, word := range strings.Fields(strings.ToLower(q.Text)) {
		if !strings.Contains(e.hay, word) {
			return false
		}
	}
	return true
}

// runListOf builds the list for the query in the Params status, q, realm
// and cluster.
func runListOf(v *View) (*runList, error) {
	q := runQuery{Status: v.Params.Get("status"), Text: strings.TrimSpace(v.Params.Get("q")), Realm: v.Params.Get("realm"), Cluster: v.Params.Get("cluster")}
	if !slices.ContainsFunc(runFilters, func(f struct{ Key, Label string }) bool { return f.Key == q.Status }) {
		return nil, fmt.Errorf("status: unknown filter: %w", ErrBadRequest)
	}
	list := &runList{Total: len(v.Snap.Runs)}
	if q.Text != "" || q.Realm != "" || q.Cluster != "" {
		list.Clear = runQuery{Status: q.Status}.href()
	}
	var picked []runEntry
	for _, run := range v.Snap.Runs {
		if e := runEntryOf(v.Snap, run); e.matches(runQuery{Text: q.Text}) {
			picked = append(picked, e)
		}
	}
	for _, f := range runFilters {
		n := 0
		for _, e := range picked {
			if e.matches(q) && runMatches(e.run, f.Key) {
				n++
			}
		}
		tab := q
		tab.Status = f.Key
		list.Tabs = append(list.Tabs, runTab{Label: f.Label, Href: tab.href(), N: n, On: f.Key == q.Status})
	}
	list.Realms, list.Clusters = runChips(picked, q)
	for _, e := range picked {
		if !e.matches(q) || !runMatches(e.run, q.Status) {
			continue
		}
		if len(list.Rows) == runListMax {
			list.More++
			continue
		}
		run := e.run
		row := runListRow{
			ID: run.ID, Title: e.title, Realm: e.realm, Chip: runSourceChip(runSource(v.Snap, run)),
			Started: runWhen(run.Start, v.Now), Elapsed: runDur(runElapsed(run, v.Now)),
			Since: runSince(run),
		}
		row.Result, row.ResultClass = runResult(run, v.Now)
		row.Pill, row.PillClass = runPill(run, nil)
		list.Rows = append(list.Rows, row)
	}
	return list, nil
}

// runChips are the chips of the realms and the clusters of the runs that
// match the status and the text of the query, each with how many of them
// it holds, the clusters only those of the realm the query picked. A chip
// that is on turns its filter off. The realms show when there is more than
// one, or one is picked.
func runChips(entries []runEntry, q runQuery) (realms, clusters []runTab) {
	realmN, clusterN := map[string]int{}, map[string]int{}
	clusterRealm := map[string]string{}
	for _, e := range entries {
		if !runMatches(e.run, q.Status) {
			continue
		}
		for _, realm := range e.realms {
			realmN[realm]++
		}
		if e.cluster != "" && (q.Realm == "" || slices.Contains(e.realms, q.Realm)) {
			clusterN[e.cluster]++
			if prev, seen := clusterRealm[e.cluster]; !seen || prev == e.realm {
				clusterRealm[e.cluster] = e.realm
			} else {
				clusterRealm[e.cluster] = ""
			}
		}
	}
	if len(realmN) > 1 || q.Realm != "" {
		for _, realm := range slices.Sorted(maps.Keys(realmN)) {
			chip := q
			chip.Realm, chip.Cluster = realm, ""
			if realm == q.Realm {
				chip.Realm = ""
			}
			realms = append(realms, runTab{Label: realm, Href: chip.href(), N: realmN[realm], On: realm == q.Realm, Tip: "realm " + realm})
		}
	}
	for _, name := range slices.Sorted(maps.Keys(clusterN)) {
		chip := q
		chip.Cluster = name
		if name == q.Cluster {
			chip.Cluster = ""
		}
		tip := "cluster " + name
		if realm := clusterRealm[name]; realm != "" {
			tip += " in realm " + realm
		}
		clusters = append(clusters, runTab{Label: name, Href: chip.href(), N: clusterN[name], On: name == q.Cluster, Tip: tip})
	}
	return realms, clusters
}
