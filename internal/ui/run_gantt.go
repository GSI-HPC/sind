// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/activity"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

// The run page draws its summary and its timeline in the browser
// (gantt.js), from one block of data that the server sends whenever the run
// changes: the spans of the run, their times as milliseconds since the run
// began, and the amounts of their work over time. The browser computes
// what depends on the time it draws at, such as how long a step has run,
// the scale of the axis, which hidden spans to show and how fast a pull
// goes, so that none of it waits for the next event, and no live update
// of the page cuts the motion of the timeline.

// ganttUsualRuns is how many earlier runs of the same command the usual
// durations are the median of.
const ganttUsualRuns = 5

// ganttSpan is a span of the run, as gantt.js reads it. The times are
// milliseconds since the run began; R is nil for a span that has not begun
// to run, E for one that has not ended.
type ganttSpan struct {
	ID     string `json:"i"`
	Parent string `json:"p,omitempty"`
	Kind   string `json:"k"`
	Name   string `json:"n"`
	Node   string `json:"node,omitempty"`
	Role   string `json:"role,omitempty"`
	// Hidden is the span's flag: gantt.js shows such a span, other than a
	// call, once it failed or has run for activity.RevealAfter.
	Hidden bool   `json:"h,omitempty"`
	Q      int64  `json:"q"`
	R      *int64 `json:"r,omitempty"`
	E      *int64 `json:"e,omitempty"`
	// Class is how the span stands (runSpanClass), for a span that ended
	// or a run that ended; gantt.js tells queued from running by R.
	Class   string `json:"c"`
	Total   int    `json:"tot,omitempty"`
	Unit    string `json:"u,omitempty"`
	Amount  int64  `json:"a,omitempty"`
	Size    int64  `json:"z,omitempty"`
	Msg     string `json:"m,omitempty"`
	Err     string `json:"err,omitempty"`
	ErrKind string `json:"ec,omitempty"`
	Timeout int64  `json:"to,omitempty"`
	// W are the samples of its work: milliseconds, amount, size.
	W [][3]int64 `json:"w,omitempty"`
}

// runGantt is the data of the run page's summary and timeline.
type runGantt struct {
	// Origin is when the run began, in milliseconds since 1970; 0 for a
	// run without any start.
	Origin int64 `json:"origin"`
	// End is when the run ended, Last its newest event, in milliseconds
	// since Origin.
	End  *int64 `json:"end,omitempty"`
	Last int64  `json:"last"`
	// State is the run's class (runStatusClass), Status its status, and
	// Suspended, Stopping and Unfinished what its pill says.
	State      string `json:"state"`
	Status     string `json:"status,omitempty"`
	Suspended  bool   `json:"suspended,omitempty"`
	Stopping   bool   `json:"stopping,omitempty"`
	Unfinished bool   `json:"unfinished,omitempty"`
	Title      string `json:"title"`
	// Spans are the command and the spans below it, parents first: every
	// span but the calls, and the calls of a target, which its row draws
	// as what the target does.
	Spans []ganttSpan `json:"spans"`
	// Usual is the median duration of each step in the earlier runs of
	// the same command that ended ok and pulled as this one does, and
	// UsualRun that of the runs, in milliseconds; UsualN is how many runs
	// they come from, 0 for none.
	Usual    map[string]int64 `json:"usual,omitempty"`
	UsualRun int64            `json:"usualRun,omitempty"`
	UsualN   int              `json:"usualN,omitempty"`
}

// runGanttOf builds the data of the run page's summary and timeline.
func runGanttOf(rc runCtx) *runGantt {
	run := rc.Run
	origin := runOrigin(run)
	since := func(t time.Time) int64 { return max(t.Sub(origin), 0).Milliseconds() }
	g := &runGantt{
		Origin: origin.UnixMilli(), Last: since(run.Last), State: runStatusClass(run), Status: run.Status,
		Suspended: run.Suspended, Stopping: !run.Done() && rc.Task != nil && rc.Task.Canceling,
		Unfinished: run.Unfinished, Title: run.Title,
	}
	if origin.IsZero() {
		g.Origin = 0
	}
	if run.End != nil {
		end := since(*run.End)
		g.End = &end
	}
	var add func(s store.Span, parentKind string)
	add = func(s store.Span, parentKind string) {
		if s.Kind == activity.KindCall && parentKind != activity.KindTarget {
			return
		}
		gs := ganttSpan{
			ID: s.ID, Parent: s.Parent, Kind: s.Kind, Name: s.Name, Node: s.Node, Role: s.Role,
			Hidden: s.Hidden, Q: since(s.Queued), Class: runSpanClass(s, run.Done()), Total: s.Total,
			Unit: s.Unit, Amount: s.Amount, Size: s.Size, Msg: s.Message, Err: s.Err, ErrKind: s.Class,
			Timeout: s.Timeout.Milliseconds(),
		}
		if !s.Started.IsZero() {
			r := since(s.Started)
			gs.R = &r
		}
		if s.Ended != nil {
			e := since(*s.Ended)
			gs.E = &e
		}
		for _, w := range s.Samples {
			gs.W = append(gs.W, [3]int64{since(w.At), w.Amount, w.Size})
		}
		g.Spans = append(g.Spans, gs)
		if s.Kind == activity.KindCall {
			return
		}
		for _, c := range s.Children {
			add(c, s.Kind)
		}
	}
	add(run.Root, "")
	g.Usual, g.UsualRun, g.UsualN = runUsual(rc.Snap, run)
	return g
}

// JSON is the data as gantt.js reads it, from an attribute of the page.
func (g *runGantt) JSON() string {
	b, _ := json.Marshal(g)
	return string(b)
}

// runHasStep reports whether a step of the name is a child of the run's
// command.
func runHasStep(run store.Run, name string) bool {
	return slices.ContainsFunc(run.Root.Children, func(s store.Span) bool {
		return s.Kind == activity.KindStep && s.Name == name
	})
}

// runUsual returns the median duration of each step, and of the whole
// run, in the newest earlier runs of the same command that ended ok, at
// most ganttUsualRuns of them, and how many runs that is. A run that
// pulls images takes much longer than one that does not, so only runs
// that pulled as this one does so far count.
func runUsual(snap store.Snapshot, run store.Run) (map[string]int64, int64, int) {
	pulls := runHasStep(run, "pull images")
	steps := map[string][]int64{}
	var runs []int64
	for _, o := range snap.Runs {
		if len(runs) == ganttUsualRuns {
			break
		}
		if o.ID == run.ID || o.Title != run.Title || !o.Done() || o.Status != store.StatusOK || runHasStep(o, "pull images") != pulls {
			continue
		}
		runs = append(runs, runElapsed(o, *o.End).Milliseconds())
		for _, s := range o.Root.Children {
			if s.Kind == activity.KindStep && !s.Started.IsZero() && s.Ended != nil {
				steps[s.Name] = append(steps[s.Name], s.Ended.Sub(s.Started).Milliseconds())
			}
		}
	}
	if len(runs) == 0 {
		return nil, 0, 0
	}
	usual := make(map[string]int64, len(steps))
	for name, d := range steps {
		usual[name] = runMedian(d)
	}
	return usual, runMedian(runs), len(runs)
}

// runMedian returns the median of a list that is not empty: the lower of
// the two in the middle of an even one.
func runMedian(d []int64) int64 {
	d = slices.Clone(d)
	slices.Sort(d)
	return d[(len(d)-1)/2]
}
