// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/GSI-HPC/sind/internal/ui/activity"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

// runListMax is the most runs the Activity page lists.
const runListMax = 500

// runFilters are the filters of the Activity page, in the order of its tabs.
var runFilters = []struct{ Key, Label string }{
	{"", "All"}, {"running", "Running"}, {"ok", "OK"}, {"failed", "Failed"}, {"canceled", "Canceled"},
}

// registerRunActivity adds the Activity page and its list of runs.
func registerRunActivity(r *Registry) {
	r.AddFragment(Fragment{
		Name:   "run-list",
		Topics: []store.Topic{store.TopicRuns, store.TopicTasks},
		Params: []string{"status"},
		Load: func(v *View) error {
			list, err := runListOf(v)
			v.Data = list
			return err
		},
	})
	r.AddPage(Page{
		Name: "run-activity", Pattern: "GET /activity", Title: "Activity", Nav: "activity",
		Fragments: []string{"run-list"},
	})
	r.SetNavCount("activity", runNavCount)
}

// runNavCount is the count behind the Activity entry of the rail: the runs
// that go on, or else how many the log holds.
func runNavCount(snap store.Snapshot) NavCount {
	running := 0
	for _, run := range snap.Runs {
		if !run.Done() {
			running++
		}
	}
	switch {
	case running > 0:
		return NavCount{Text: strconv.Itoa(running) + " running", Class: "run"}
	case len(snap.Runs) > 0:
		return NavCount{Text: strconv.Itoa(len(snap.Runs))}
	}
	return NavCount{}
}

// runTab is a filter tab with the number of runs it holds.
type runTab struct {
	Label string
	Href  string
	N     int
	On    bool
}

// runListRow is a run in the list.
type runListRow struct {
	ID        string
	Title     string
	Pill      string
	PillClass string
	// Chip says where a run came from that sind ui did not start.
	Chip    string
	Started string
	Elapsed string
	Since   int64
	Counts  string
}

// runList is the Activity page's list.
type runList struct {
	Tabs []runTab
	Rows []runListRow
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

// runListOf builds the list for the filter in the Param status.
func runListOf(v *View) (*runList, error) {
	filter := v.Params.Get("status")
	if !slices.ContainsFunc(runFilters, func(f struct{ Key, Label string }) bool { return f.Key == filter }) {
		return nil, fmt.Errorf("status: unknown filter: %w", ErrBadRequest)
	}
	list := &runList{Total: len(v.Snap.Runs)}
	for _, f := range runFilters {
		n := 0
		for _, run := range v.Snap.Runs {
			if runMatches(run, f.Key) {
				n++
			}
		}
		href := "/activity"
		if f.Key != "" {
			href += "?status=" + f.Key
		}
		list.Tabs = append(list.Tabs, runTab{Label: f.Label, Href: href, N: n, On: f.Key == filter})
	}
	for _, run := range v.Snap.Runs {
		if !runMatches(run, filter) {
			continue
		}
		if len(list.Rows) == runListMax {
			list.More++
			continue
		}
		task, hasTask := runTaskOf(v.Snap, run.Trace)
		row := runListRow{
			ID: run.ID, Title: run.Title, Chip: runSourceChip(runSource(v.Snap, run)),
			Started: runWhen(run.Start, v.Now), Elapsed: runDur(runElapsed(run, v.Now)),
			Since: runSince(run), Counts: runCountsText(run),
		}
		if hasTask {
			row.Title = runTitle(run, &task)
		}
		if row.Title == "" {
			row.Title = run.ID
		}
		row.Pill, row.PillClass = runPill(run, nil)
		list.Rows = append(list.Rows, row)
	}
	return list, nil
}

// runCountsText sums up a run's targets: "3 ok · 1 failed", "–" for none.
func runCountsText(run store.Run) string {
	c := activity.CountTargets(run.Root)
	var parts []string
	for _, p := range []struct {
		n    int
		what string
	}{{c.OK, "ok"}, {c.Running, "running"}, {c.Queued, "queued"}, {c.Failed, "failed"}, {c.Canceled, "canceled"}, {c.Skipped, "skipped"}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.what))
		}
	}
	if len(parts) == 0 {
		return "–"
	}
	return strings.Join(parts, " · ")
}
