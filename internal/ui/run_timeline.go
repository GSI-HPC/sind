// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/activity"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

// maxTimelineDepth bounds how deep the rows of the timeline nest below a
// step: steps, batches, targets.
const maxTimelineDepth = 3

// runTick is a label on the time axis of the timeline.
type runTick struct {
	Label string
	// Pos is the place on the axis, in percent.
	Pos int
	// Minor marks a label that a narrow screen leaves out: all but the
	// first and the last, and the middle one of an odd number.
	Minor bool
}

// runRow is a row of the timeline: a span and its bar.
type runRow struct {
	// ID names the row, so that a live update morphs it into the same
	// element and a row that the user opened stays open.
	ID    string
	Label string
	Name  string
	Class string
	Mark  string
	Kind  string
	// Total is the number of targets a step expects; 0 shows none.
	Total int
	// Bar is set for a span that has begun; Left and Width are percent.
	Bar         bool
	Left, Width int
	Result      string
	Children    []runRow
}

// runTimeline is the Gantt chart of the run's steps.
type runTimeline struct {
	Ticks []runTick
	Rows  []runRow
}

// runTickSteps are the tick distances the axis picks from, the smallest one
// that gives at most runMaxTicks labels.
var runTickSteps = []time.Duration{
	time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second,
	time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 30 * time.Minute,
	time.Hour, 2 * time.Hour, 6 * time.Hour, 24 * time.Hour,
}

const runMaxTicks = 8

// runTimelineOf builds the timeline: a row for each visible span below the
// command, children of a step below it, bars scaled to the run's clock. The
// scale is at least eight seconds, and grows with a run that goes on.
func runTimelineOf(rc runCtx) *runTimeline {
	run := rc.Run
	origin := runOrigin(run)
	end := runEnd(run, rc.Now)
	scale := max(end.Sub(origin), 8*time.Second)
	if origin.IsZero() {
		scale = 8 * time.Second
	}
	tl := &runTimeline{}
	step := runTickSteps[len(runTickSteps)-1]
	for _, s := range runTickSteps {
		if int(scale/s) <= runMaxTicks {
			step = s
			break
		}
	}
	for t := time.Duration(0); t <= scale && len(tl.Ticks) <= runMaxTicks; t += step {
		tl.Ticks = append(tl.Ticks, runTick{Label: strings.Replace(runDur(t), ".0s", "s", 1), Pos: runPct(float64(t), float64(scale))})
	}
	n := len(tl.Ticks)
	for i := range tl.Ticks {
		// The middle label stays when it is in the middle, between as many
		// labels on each side; next to the last one it would touch it.
		tl.Ticks[i].Minor = i != 0 && i != n-1 && (n%2 == 0 || i != n/2)
	}
	for _, s := range activity.VisibleChildren(run.Root) {
		tl.Rows = append(tl.Rows, runRowOf(s, run, origin, end, scale, rc.Now, 1))
	}
	return tl
}

// runRowOf builds the row of a span, and the rows of its targets.
func runRowOf(s store.Span, run store.Run, origin, end time.Time, scale time.Duration, now time.Time, depth int) runRow {
	class := runSpanClass(s, run.Done())
	row := runRow{
		ID: s.ID, Label: s.Name, Name: runLabel(s), Class: class, Mark: runMark(class), Kind: s.Kind,
		Total: s.Total,
	}
	if !s.Started.IsZero() && !origin.IsZero() {
		to := end
		if s.Ended != nil {
			to = *s.Ended
		}
		from := max(s.Started.Sub(origin), 0)
		row.Bar = true
		row.Left = runPct(float64(from), float64(scale))
		row.Width = min(runPct(float64(max(to.Sub(s.Started), 0)), float64(scale)), 100-row.Left)
	}
	row.Result = runRowResult(s, class, now)
	if depth < maxTimelineDepth {
		for _, c := range activity.VisibleChildren(s) {
			if c.Kind == activity.KindTarget || c.Kind == activity.KindBatch {
				row.Children = append(row.Children, runRowOf(c, run, origin, end, scale, now, depth+1))
			}
		}
	}
	return row
}

// runLabel is what a row of a span is called: the node of a target, the
// batch's place such as "2/5", the name of anything else.
func runLabel(s store.Span) string {
	switch {
	case s.Kind == activity.KindTarget:
		return runName(s)
	case s.Kind == activity.KindBatch && s.Batch != "":
		return strings.TrimSpace(s.Name + " " + s.Batch)
	}
	return s.Name
}

// runRowResult is the text at the end of a row: how it stands or how long it
// took, with the counts of its targets.
func runRowResult(s store.Span, class string, now time.Time) string {
	elapsed := runDur(activity.Elapsed(s, now))
	switch {
	case class == runQueued:
		return "queued"
	case class == runSkipped:
		return "skipped"
	case s.Kind == activity.KindTarget || s.Kind == activity.KindBatch:
		if class == runRunning {
			if d := runDetail(s); d != "" {
				return d
			}
		}
		return elapsed
	}
	c := activity.CountTargets(s)
	if c.Total == 0 && s.Total == 0 {
		return elapsed
	}
	if class == runRunning {
		done, total := runStepCounts(s)
		return fmt.Sprintf("%d/%d · %s", done, total, elapsed)
	}
	parts := []string{elapsed}
	for _, p := range []struct {
		n    int
		what string
	}{{c.OK, "ok"}, {c.Failed, "failed"}, {c.Canceled, "canceled"}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.what))
		}
	}
	return strings.Join(parts, " · ")
}
