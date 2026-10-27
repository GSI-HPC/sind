// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/activity"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

// The progress display (the run page, the task dock and the Activity page)
// draws what the event log tells about a run. This file holds what the
// three share: how a duration and a state are written, the bar of steps and
// the line of the step that goes on. Every function is a pure function of
// its arguments, so equal input gives equal HTML and the live stream can
// tell when nothing changed.

// Classes of a span as the display colours it: the status of an ended span,
// queued or running otherwise, skipped for work that a finished run never
// began, and ended for a status the log does not name.
const (
	runQueued   = "queued"
	runRunning  = "running"
	runOK       = "ok"
	runFailed   = "failed"
	runCanceled = "canceled"
	runSkipped  = "skipped"
	runEnded    = "ended"
)

// runMarks are the characters before a span's name.
var runMarks = map[string]string{
	runOK: "✓", runFailed: "✗", runCanceled: "~", runRunning: "▸",
	runQueued: "·", runSkipped: "–", runEnded: "✓",
}

// runMark returns the mark of a class.
func runMark(class string) string { return runMarks[class] }

// runDur writes a duration the way the terminal does: 3.4s below ten
// seconds, 42s, 5m07s, 2h03m. The script of the run page writes the same
// text, so that a clock it moves does not jump when the page is rendered
// again.
func runDur(d time.Duration) string {
	d = max(d, 0)
	switch {
	case d < 10*time.Second:
		t := int(d / (100 * time.Millisecond))
		return fmt.Sprintf("%d.%ds", t/10, t%10)
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d/time.Minute), int(d%time.Minute/time.Second))
	}
	return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
}

// runPct returns num/den as a whole percent from 0 to 100; 0 for a den that
// is not positive.
func runPct(num, den float64) int {
	if den <= 0 || num <= 0 {
		return 0
	}
	return int(min(num/den*100+.5, 100))
}

// runFine returns num/den in hundredths of a percent, from 0 to 10000, for
// the width that run.js gives a bar (data-fill), finer than the whole
// percent of its class; 0 for a den that is not positive.
func runFine(num, den float64) int {
	if den <= 0 || num <= 0 {
		return 0
	}
	return int(min(num/den*10000+.5, 10000))
}

// runOrigin is the time that a run's clock starts at: the start of its
// command span, or of the first span that began when the log gave no start
// for the command. It is the zero time for a run without any.
func runOrigin(run store.Run) time.Time {
	for _, t := range []time.Time{run.Start, run.Root.Started, run.Root.Queued} {
		if !t.IsZero() {
			return t
		}
	}
	first := time.Time{}
	var scan func(s store.Span)
	scan = func(s store.Span) {
		for _, c := range s.Children {
			if !c.Started.IsZero() && (first.IsZero() || c.Started.Before(first)) {
				first = c.Started
			}
			scan(c)
		}
	}
	scan(run.Root)
	return first
}

// runEnd is the time that a run's clock stops at: the end of the run, or
// now while it goes on. A run that is over has the time of its newest event
// if the log never ended its command.
func runEnd(run store.Run, now time.Time) time.Time {
	if run.End != nil {
		return *run.End
	}
	return now
}

// runElapsed is how long a run has taken: zero for a run without a start.
func runElapsed(run store.Run, now time.Time) time.Duration {
	origin := runOrigin(run)
	if origin.IsZero() {
		return 0
	}
	return max(runEnd(run, now).Sub(origin), 0)
}

// runTicks reports whether the clock of a run goes on by itself: while the
// run runs and is not suspended.
func runTicks(run store.Run) bool { return !run.Done() && !run.Suspended }

// runSince returns the unix time in milliseconds that run.js counts the
// clock of a run from, 0 for a clock that stands still.
func runSince(run store.Run) int64 {
	if !runTicks(run) {
		return 0
	}
	origin := runOrigin(run)
	if origin.IsZero() {
		return 0
	}
	return origin.UnixMilli()
}

// runSpanClass returns the class of a span: how it stands, and for a span of
// a run that has ended what became of it.
func runSpanClass(s store.Span, runDone bool) string {
	switch {
	case s.Done() || s.State == activity.StateEnded:
		switch s.Status {
		case store.StatusOK:
			return runOK
		case store.StatusFailed:
			return runFailed
		case store.StatusCanceled:
			return runCanceled
		case store.StatusSkipped:
			return runSkipped
		}
		return runEnded
	case runDone && s.State == activity.StateQueued:
		return runSkipped
	case runDone:
		return runCanceled
	case s.State == activity.StateQueued || (s.State == "" && s.Started.IsZero()):
		return runQueued
	}
	return runRunning
}

// runStatusClass returns the class of a run: how its command span stands.
func runStatusClass(run store.Run) string {
	if !run.Done() {
		return runRunning
	}
	switch run.Status {
	case store.StatusOK:
		return runOK
	case store.StatusFailed:
		return runFailed
	case store.StatusCanceled:
		return runCanceled
	}
	return runEnded
}

// runPill returns the label and the pill class of a run's state. A task
// that is being stopped says so until the run ends.
func runPill(run store.Run, task *store.Task) (label, class string) {
	switch c := runStatusClass(run); {
	case c == runRunning && run.Suspended:
		return "suspended", "warn"
	case c == runRunning && task != nil && task.Canceling:
		return "stopping", "warn"
	case c == runRunning:
		return "running", "run"
	case c == runOK:
		return "ok", "ok"
	case c == runFailed:
		return "failed", "bad"
	case c == runCanceled:
		return "canceled", "warn"
	case run.Unfinished && run.Status == "":
		return "no end recorded", "warn"
	}
	if run.Status != "" {
		return run.Status, ""
	}
	return "ended", ""
}

// runTaskOf returns the newest task that started a command of the trace.
func runTaskOf(snap store.Snapshot, trace string) (store.Task, bool) {
	if trace == "" {
		return store.Task{}, false
	}
	for i := len(snap.Tasks) - 1; i >= 0; i-- {
		if snap.Tasks[i].Trace == trace {
			return snap.Tasks[i], true
		}
	}
	return store.Task{}, false
}

// Where a run came from, as runSource tells it.
const (
	runFromUI       = "ui"
	runFromTerminal = "terminal"
	runFromProgram  = "program"
)

// runSource tells where a run came from: from sind ui when this sind ui has
// its task or its tracestate says that a sind ui started it; from another
// program when it continues a trace that its caller handed on, as a CI job
// or an MCP client does; else from a terminal.
func runSource(snap store.Snapshot, run store.Run) string {
	if _, ok := runTaskOf(snap, run.Trace); ok || run.FromUI {
		return runFromUI
	}
	if run.Parent != "" {
		return runFromProgram
	}
	return runFromTerminal
}

// runLockWait is the name of the wait of a command for the lock of its
// realm.
const runLockWait = "realm lock"

// runWaitsForLock reports whether a run that goes on waits for the lock of
// its realm, so that it has not changed anything yet.
func runWaitsForLock(run store.Run) bool {
	if run.Done() {
		return false
	}
	for _, s := range activity.VisibleChildren(run.Root) {
		if s.Kind == activity.KindWait && s.Name == runLockWait && !s.Done() {
			return true
		}
	}
	return false
}

// runStop is the Stop button of a task that runs, with the question that
// it asks in the page before it stops the command.
type runStop struct {
	TaskID string
	Title  string
	// Text says what stays when the command stops.
	Text string
	// CSRF is the session's token, for the form.
	CSRF string
	// Small makes the button small, as in the dock.
	Small bool
}

// newRunStop returns the Stop button of the task t, whose run may be nil,
// with the command's title; nil for a task that has ended or is
// being stopped.
func newRunStop(t store.Task, run *store.Run, title, csrf string, small bool) *runStop {
	if t.Done() || t.Canceling || (run != nil && run.Done()) {
		return nil
	}
	return &runStop{TaskID: t.ID, Title: title, Text: runStopText(t.Action, run), CSRF: csrf, Small: small}
}

// runStopText says what stays when a command of the action stops: sind
// stops it as Ctrl-C does, and a command that makes things removes what it
// has made, while one that removes or changes things leaves what it has
// done. A command that still waits for the lock of its realm has done
// nothing.
func runStopText(action string, run *store.Run) string {
	if run != nil && runWaitsForLock(*run) {
		return "It still waits for the lock of its realm and has changed nothing yet."
	}
	switch action {
	case "create-cluster":
		return "sind removes the nodes it has made so far (rollback). The saved config stays."
	case "create-worker":
		return "sind removes the workers it has added so far (rollback)."
	case "delete-cluster":
		return "What sind has removed so far stays removed. The rest of the cluster stays, and you can delete it again."
	case "delete-worker":
		return "The workers that sind has removed stay removed. The others stay."
	case "delete-realm":
		return "What sind has removed so far stays removed. The rest of the realm stays, and you can delete it again."
	case "power":
		return "The nodes that sind has handled stay as they are now. The others stay as they were."
	}
	return "sind stops the command, as Ctrl-C does in a terminal."
}

// runSourceText says where a run came from, for its page.
func runSourceText(source string) string {
	switch source {
	case runFromUI:
		return "from sind ui"
	case runFromProgram:
		return "from another program"
	}
	return "from a terminal"
}

// runSourceChip is the chip of a run in a list: none for a run of sind ui.
func runSourceChip(source string) string {
	switch source {
	case runFromTerminal:
		return "terminal"
	case runFromProgram:
		return "other program"
	}
	return ""
}

// runTitle is the title of a run that task t started, with the cluster the
// task works on, which the log of an older sind does not name.
func runTitle(run store.Run, t *store.Task) string {
	if run.Title == "" || t.Cluster == "" || strings.HasSuffix(run.Title, " "+t.Cluster) {
		return run.Title
	}
	return run.Title + " " + t.Cluster
}

// runSeg is a segment of the bar that shows a run's steps.
type runSeg struct {
	Class string
	Title string
	// Fill is the part of a running segment that is done, in percent, and
	// FillFine in hundredths of a percent (runFine).
	Fill, FillFine int
}

// runBar is the bar of a run's steps, a segment for each step.
type runBar []runSeg

// runSegDone reports whether a segment stands for a step that is over.
func runSegDone(class string) bool {
	switch class {
	case runOK, runFailed, runCanceled, runSkipped, runEnded:
		return true
	}
	return false
}

// Pct is the part of the bar that is done, in whole percent: the steps that
// are over, and the part of the running ones that is done. It is the value
// of the bar's progressbar role.
func (b runBar) Pct() int {
	if len(b) == 0 {
		return 0
	}
	sum := 0
	for _, s := range b {
		switch {
		case runSegDone(s.Class):
			sum += 100
		case s.Class == runRunning:
			sum += s.Fill
		}
	}
	return sum / len(b)
}

// Text says how far the bar is in words, for assistive technology: "2 of 4
// steps done", or the state of a run without steps.
func (b runBar) Text() string {
	if len(b) == 1 && !strings.Contains(b[0].Title, ":") {
		return b[0].Title
	}
	done := 0
	for _, s := range b {
		if runSegDone(s.Class) {
			done++
		}
	}
	return fmt.Sprintf("%d of %d steps done", done, len(b))
}

// runSayState is the state of a command as the announcements tell it: its
// class, or stopping while a stop is under way.
func runSayState(class string, canceling bool) string {
	if class == runRunning && canceling {
		return "stopping"
	}
	return class
}

// runSay is the one sentence that the live region of a page gets when a
// command starts, is being stopped or ends: "create worker dev: done in
// 12s." A failure adds why, when it is known.
func runSay(title, state, elapsed, why string) string {
	switch state {
	case runRunning:
		return title + ": started."
	case "stopping":
		return title + ": stopping."
	case runOK:
		return title + ": done in " + elapsed + "."
	case runFailed:
		if why != "" {
			return title + ": failed. " + why
		}
		return title + ": failed."
	case runCanceled:
		return title + ": stopped."
	}
	return title + ": ended."
}

// runFailure says in one line why a run failed: the error of its first
// target that failed, else the error of its command.
func runFailure(run store.Run) string {
	if f := activity.FailedTargets(run.Root); len(f) > 0 {
		if len(f[0].Nodes) == 1 {
			return strings.ReplaceAll(f[0].Err, "{}", f[0].Nodes[0])
		}
		return runFoldNodes(f[0].Nodes) + ": " + f[0].Err
	}
	return run.Err
}

// runStepCounts returns how many targets of the step s are done and how many
// it expects: its own total, or the targets seen if there are more.
func runStepCounts(s store.Span) (done, total int) {
	c := activity.CountTargets(s)
	return c.Done(), max(s.Total, c.Total)
}

// runSegs returns the segments of a run: one for each visible step, in the
// order they started. A run without steps has one segment for the command.
func runSegs(run store.Run) runBar {
	var segs runBar
	for _, s := range activity.VisibleChildren(run.Root) {
		if s.Kind != activity.KindStep {
			continue
		}
		seg := runSeg{Class: runSpanClass(s, run.Done()), Title: s.Name + ": " + runSpanClass(s, run.Done())}
		done, total := runStepCounts(s)
		if total > 0 {
			seg.Title += fmt.Sprintf(" (%d/%d)", done, total)
		}
		if seg.Class == runRunning {
			switch {
			case total > 0:
				part := float64(done) + runPartial(s)
				seg.Fill, seg.FillFine = runPct(part, float64(total)), runFine(part, float64(total))
			case s.Size > 0:
				seg.Fill, seg.FillFine = runPct(float64(s.Amount), float64(s.Size)), runFine(float64(s.Amount), float64(s.Size))
			}
		}
		segs = append(segs, seg)
	}
	if len(segs) == 0 {
		class := runStatusClass(run)
		title := "starting"
		if run.Done() {
			title = class
		}
		segs = append(segs, runSeg{Class: class, Title: title})
	}
	return segs
}

// runPartial sums the part that the running targets below s have done of
// their own work, as fractions: a pull that is half way counts as 0.5.
func runPartial(s store.Span) float64 {
	sum := 0.0
	runVisit(s, func(t store.Span, _ string) {
		if t.Kind == activity.KindTarget && !t.Done() && t.Size > 0 {
			sum += min(float64(t.Amount)/float64(t.Size), 1)
		}
	})
	return sum
}

// runDetail says what a span that goes on is doing now: its message, or the
// visible span below it that goes on with its message, or how far its
// transfer is.
func runDetail(s store.Span) string {
	if s.Message != "" {
		return s.Message
	}
	for _, c := range activity.VisibleChildren(s) {
		if c.Done() || c.State == activity.StateQueued {
			continue
		}
		if c.Kind == activity.KindBatch {
			if d := runDetail(c); d != "" {
				return d
			}
			continue
		}
		return strings.TrimSpace(c.Name + " " + c.Message)
	}
	return runAmount(s)
}

// runRunningTarget returns the first target of s that runs, looking through
// batches, and the first one that has something to say if any has.
func runRunningTarget(s store.Span) (store.Span, bool) {
	var first store.Span
	found := false
	var scan func(p store.Span) (store.Span, bool)
	scan = func(p store.Span) (store.Span, bool) {
		for _, c := range activity.VisibleChildren(p) {
			if c.Kind == activity.KindBatch {
				if t, ok := scan(c); ok {
					return t, true
				}
				continue
			}
			if c.Kind != activity.KindTarget || c.State != activity.StateRunning || c.Done() {
				continue
			}
			if runDetail(c) != "" {
				return c, true
			}
			if !found {
				first, found = c, true
			}
		}
		return store.Span{}, false
	}
	if t, ok := scan(s); ok {
		return t, true
	}
	return first, found
}

// runName is what a target is called: its node, or its name.
func runName(s store.Span) string {
	if s.Node != "" {
		return s.Node
	}
	return s.Name
}

// runStepNow says what a running step does: "nodes 3/8 · worker-2 ready
// munge".
func runStepNow(s store.Span) string {
	out := s.Name
	if done, total := runStepCounts(s); total > 0 {
		out += fmt.Sprintf(" %d/%d", done, total)
	}
	switch t, ok := runRunningTarget(s); {
	case ok:
		out += " · " + strings.TrimSpace(runName(t)+" "+runDetail(t))
	case s.Message != "":
		out += " · " + s.Message
	default:
		if d := runDetail(s); d != "" {
			out += " · " + d
		}
	}
	return out
}

// runLine is the line under a run's bar: what goes on now, or how it ended.
func runLine(run store.Run, task *store.Task, now time.Time) string {
	elapsed := runDur(runElapsed(run, now))
	if run.Done() {
		switch {
		case run.Unfinished && run.Status == "":
			return "no end recorded; the last event came after " + elapsed
		case run.Status == "":
			return "ended in " + elapsed
		case run.Status == store.StatusOK:
			return "done in " + elapsed
		}
		return run.Status + " in " + elapsed
	}
	if run.Suspended {
		return "suspended"
	}
	if task != nil && task.Canceling {
		for _, s := range activity.VisibleChildren(run.Root) {
			if s.Name == "rollback" && s.State == activity.StateRunning && !s.Done() {
				return "rolling back"
			}
		}
		c := activity.CountTargets(run.Root)
		return fmt.Sprintf("stopping · %d running will stop · %d queued will not start", c.Running, c.Queued)
	}
	var parts []string
	for _, s := range activity.VisibleChildren(run.Root) {
		if s.Done() || s.State != activity.StateRunning {
			continue
		}
		switch s.Kind {
		case activity.KindStep:
			parts = append(parts, runStepNow(s))
		case activity.KindWait:
			parts = append(parts, strings.TrimSpace("waiting for "+s.Name+" "+s.Message))
		default:
			parts = append(parts, strings.TrimSpace(s.Name+" "+s.Message))
		}
	}
	switch {
	case len(parts) > 0:
		return strings.Join(parts, "  |  ")
	case run.Root.Message != "":
		return run.Root.Message
	case len(run.Root.Children) == 0:
		return "starting"
	}
	return "working"
}

// runAmount writes the work a span has done of the work it has to do, in
// its unit: "34 MB / 96 MB", "3 / 7 items", "42 %". Bytes that move have
// their rate too: "34 MB / 96 MB · 12 MB/s". It is empty for a span
// without a unit or without any work.
func runAmount(s store.Span) string {
	if s.Unit == "" || (s.Size <= 0 && s.Amount <= 0) {
		return ""
	}
	switch s.Unit {
	case "bytes":
		out := runBytes(s.Amount)
		if s.Size > 0 {
			out += " / " + runBytes(s.Size)
		}
		if s.Rate > 0 && !s.Done() {
			out += " · " + runBytes(int64(s.Rate)) + "/s"
		}
		return out
	case "percent":
		if s.Size > 0 && s.Size != 100 {
			return fmt.Sprintf("%d %%", runPct(float64(s.Amount), float64(s.Size)))
		}
		return fmt.Sprintf("%d %%", s.Amount)
	}
	if s.Size > 0 {
		return fmt.Sprintf("%d / %d %s", s.Amount, s.Size, s.Unit)
	}
	return fmt.Sprintf("%d %s", s.Amount, s.Unit)
}

// runBytes writes a size in decimal units, as docker does.
func runBytes(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d B", max(n, 0))
	}
	units := []string{"kB", "MB", "GB"}
	f, i := float64(n)/1000, 0
	for f >= 1000 && i < len(units)-1 {
		f /= 1000
		i++
	}
	if f < 10 {
		return fmt.Sprintf("%.1f %s", f, units[i])
	}
	return fmt.Sprintf("%.0f %s", f, units[i])
}

// runSpanPct is the part of its work that a span has done, in percent and
// in hundredths of a percent (runFine), and whether the span knows its
// size. A span that ended ok has done all.
func runSpanPct(s store.Span) (pct, fine int, ok bool) {
	if s.Size <= 0 {
		return 0, 0, false
	}
	if s.Done() && s.Status == store.StatusOK {
		return 100, 10000, true
	}
	return runPct(float64(s.Amount), float64(s.Size)), runFine(float64(s.Amount), float64(s.Size)), true
}

// runVisit calls fn with each visible span below s, depth first, in start
// order, and the name of the step it belongs to. The spans below a hidden
// span are hidden with it.
func runVisit(s store.Span, fn func(sp store.Span, step string)) {
	var walk func(p store.Span, step string)
	walk = func(p store.Span, step string) {
		for _, c := range activity.VisibleChildren(p) {
			st := step
			if c.Kind == activity.KindStep {
				st = c.Name
			}
			fn(c, st)
			walk(c, st)
		}
	}
	walk(s, "")
}
