// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/GSI-HPC/sind/internal/ui/activity"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

// runPipe is a segment of a node's pipeline: one span below the node's
// target.
type runPipe struct {
	Class string
	Label string
	Title string
}

// runRing is the wait ring of a node.
type runRing struct {
	// Dash is the stroke-dasharray of the arc: the used part of 69.1, the
	// circle's length.
	Dash  string
	Class string
	Title string
}

// runTile is the tile of a node.
type runTile struct {
	Name      string
	Role      string
	RoleClass string
	Class     string
	Mark      string
	Pipe      []runPipe
	Msg       string
	Ring      *runRing
}

// runNodes is the panel of node tiles.
type runNodes struct{ Tiles []runTile }

// runTileAcc collects the targets of one node, which a run may name in
// several steps (the nodes step, then the slurm step).
type runTileAcc struct {
	name, role string
	targets    []store.Span
	classes    []string
	pipe       []runPipe
	waits      []store.Span
}

// runNodesOf builds a tile for each node that the run's targets name, in
// the order they appear. A target that carries a unit is a transfer, not a
// node; it goes to the transfers panel.
func runNodesOf(rc runCtx) *runNodes {
	run := rc.Run
	var order []string
	byName := map[string]*runTileAcc{}
	runVisit(run.Root, func(t store.Span, step string) {
		if t.Kind != activity.KindTarget || t.Unit != "" || t.Size > 0 {
			return
		}
		name := runName(t)
		acc := byName[name]
		if acc == nil {
			acc = &runTileAcc{name: name}
			byName[name] = acc
			order = append(order, name)
		}
		if acc.role == "" {
			acc.role = t.Role
		}
		class := runSpanClass(t, run.Done())
		acc.targets = append(acc.targets, t)
		acc.classes = append(acc.classes, class)
		kids := runPipeKids(t)
		if len(kids) == 0 {
			acc.pipe = append(acc.pipe, runPipe{Class: class, Label: step, Title: step + ": " + class})
		}
		for _, k := range kids {
			kc := runSpanClass(k, run.Done())
			title := strings.TrimSpace(step + " · " + k.Name + " " + k.Message)
			acc.pipe = append(acc.pipe, runPipe{Class: kc, Label: k.Name, Title: title + ": " + kc})
			if k.Kind == activity.KindWait {
				acc.waits = append(acc.waits, k)
			}
		}
	})
	if len(order) == 0 {
		return nil
	}
	out := &runNodes{}
	for _, name := range order {
		out.Tiles = append(out.Tiles, runTileOf(byName[name], rc))
	}
	return out
}

// runPipeKids returns the spans that make up a target's pipeline: its
// visible calls and waits.
func runPipeKids(t store.Span) []store.Span {
	var kids []store.Span
	for _, c := range activity.VisibleChildren(t) {
		if c.Kind == activity.KindCall || c.Kind == activity.KindWait {
			kids = append(kids, c)
		}
	}
	return kids
}

// runTileClass returns how a node stands from how its targets stand: failed
// before canceled before running; a node that has some work done and some
// to come is running.
func runTileClass(classes []string) string {
	count := map[string]int{}
	for _, c := range classes {
		count[c]++
	}
	switch {
	case count[runFailed] > 0:
		return runFailed
	case count[runCanceled] > 0:
		return runCanceled
	case count[runRunning] > 0:
		return runRunning
	case count[runQueued] > 0 && count[runQueued] < len(classes):
		return runRunning
	case count[runQueued] > 0:
		return runQueued
	case count[runSkipped] == len(classes):
		return runSkipped
	}
	return runOK
}

// runTileOf builds the tile of a node.
func runTileOf(acc *runTileAcc, rc runCtx) runTile {
	t := runTile{
		Name: acc.name, Role: acc.role, RoleClass: runRoleClass(acc.role),
		Class: runTileClass(acc.classes), Pipe: acc.pipe,
	}
	t.Mark = runMark(t.Class)
	t.Ring = runRingOf(acc.waits, rc)
	switch t.Class {
	case runRunning:
		t.Msg = "running"
		for i, c := range acc.classes {
			if c != runRunning {
				continue
			}
			if d := runDetail(acc.targets[i]); d != "" {
				t.Msg = d
			}
			break
		}
		if t.Ring != nil {
			t.Msg += " · " + t.Ring.Title
		}
	case runQueued:
		t.Msg = "queued"
	case runSkipped:
		t.Msg = "did not run"
	case runCanceled:
		t.Msg = "canceled"
	case runFailed:
		t.Msg = "failed"
		for i, c := range acc.classes {
			if c == runFailed && acc.targets[i].Err != "" {
				t.Msg = runShorten(acc.targets[i].Err, 120)
				break
			}
		}
	default:
		t.Msg = "done"
	}
	return t
}

// runRingOf returns the ring of the wait of a node that has used the most
// of its limit: one that runs, or one that timed out. Nil if there is none.
func runRingOf(waits []store.Span, rc runCtx) *runRing {
	var best store.Span
	most, found := 0.0, false
	for _, w := range waits {
		used, ok := activity.WaitUsed(w, rc.Now)
		switch {
		case !ok:
			continue
		case w.Done() && (w.Status != store.StatusFailed || w.Class != "timeout"):
			continue
		case w.Done():
			used = 1
		case rc.Run.Done():
			continue
		}
		if !found || used > most {
			best, most, found = w, used, true
		}
	}
	if !found {
		return nil
	}
	class := map[string]string{activity.WaitOK: "run", activity.WaitWarn: "warn", activity.WaitCritical: "bad"}[activity.WaitLevel(most)]
	return &runRing{
		Dash:  fmt.Sprintf("%.1f 69.1", most*69.1),
		Class: class,
		Title: fmt.Sprintf("%s of %s", runDur(activity.Elapsed(best, rc.Now)), runDur(best.Timeout)),
	}
}

// runRoleClass is the class that draws a node's role stripe: the role in
// lower case with the characters a class name can hold, "" for no role.
func runRoleClass(role string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(role) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "role-" + b.String()
}

// runShorten cuts text to n characters at most, with an ellipsis, and to
// its first line.
func runShorten(text string, n int) string {
	text, _, _ = strings.Cut(text, "\n")
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	return string([]rune(text)[:n-1]) + "…"
}

// runTransfer is a span with a unit, drawn as a bar.
type runTransfer struct {
	Name   string
	Class  string
	Mark   string
	Amount string
	// Bar is set for a span that knows its size; Pct is how much is done,
	// and Fine the same in hundredths of a percent (runFine).
	Bar       bool
	Pct, Fine int
	Msg       string
}

// runOtherStep is a step that has no targets, as the rollback or the mesh
// registration.
type runOtherStep struct {
	Name      string
	Class     string
	PillClass string
	Text      string
}

// runMore is the panels of what the node tiles do not show: the transfers
// and the steps without targets.
type runMore struct {
	Transfers []runTransfer
	Steps     []runOtherStep
}

// runPillClass maps the class of a span to the class of a pill.
func runPillClass(class string) string {
	return map[string]string{runOK: "ok", runFailed: "bad", runCanceled: "warn", runRunning: "run"}[class]
}

// runMoreOf builds the transfers and the steps without targets; nil if the
// run has neither.
func runMoreOf(rc runCtx) *runMore {
	run := rc.Run
	m := &runMore{}
	runVisit(run.Root, func(s store.Span, _ string) {
		if s.Unit == "" || (s.Size <= 0 && s.Amount <= 0) {
			return
		}
		class := runSpanClass(s, run.Done())
		tr := runTransfer{Name: runName(s), Class: class, Mark: runMark(class), Amount: runAmount(s), Msg: s.Message}
		tr.Pct, tr.Fine, tr.Bar = runSpanPct(s)
		m.Transfers = append(m.Transfers, tr)
	})
	for _, s := range activity.VisibleChildren(run.Root) {
		if s.Kind != activity.KindStep || runHasTargets(s) {
			continue
		}
		class := runSpanClass(s, run.Done())
		st := runOtherStep{Name: s.Name, Class: class, PillClass: runPillClass(class)}
		switch {
		case s.Err != "":
			st.Text = runShorten(s.Err, 200)
		case class == runRunning:
			st.Text = runDetail(s)
		case s.Message != "":
			st.Text = s.Message
		case class == runQueued || class == runSkipped:
		default:
			st.Text = runDur(activity.Elapsed(s, rc.Now))
		}
		m.Steps = append(m.Steps, st)
	}
	if len(m.Transfers) == 0 && len(m.Steps) == 0 {
		return nil
	}
	return m
}

// runHasTargets reports whether a visible span below s is a target or has a
// unit, so that the node tiles or the transfers show it already.
func runHasTargets(s store.Span) bool {
	found := false
	runVisit(s, func(c store.Span, _ string) {
		found = found || c.Kind == activity.KindTarget || c.Unit != ""
	})
	return found
}

// runFact is a line of the raw facts of a run.
type runFact struct {
	Key  string
	Val  string
	Mono bool
}

// runFacts is what the log says about the run itself, for a bug report.
type runFacts struct{ Facts []runFact }

// runFactsOf builds the raw facts.
func runFactsOf(rc runCtx) *runFacts {
	run := rc.Run
	f := &runFacts{}
	add := func(key, val string, mono bool) {
		if val != "" {
			f.Facts = append(f.Facts, runFact{key, val, mono})
		}
	}
	add("Run", run.ID, true)
	add("Trace", run.Trace, true)
	add("Caller span", run.Parent, true)
	add("Program", strings.TrimSpace(run.Program+" "+run.Version), false)
	add("Started", runStamp(run.Start), true)
	if run.End != nil {
		add("Ended", runStamp(*run.End), true)
	}
	add("Last event", runStamp(run.Last), true)
	add("Status", run.Status, false)
	add("Class", run.Class, false)
	if run.Root.Exit != nil {
		add("Exit code", fmt.Sprint(*run.Root.Exit), true)
	}
	add("Error", run.Err, true)
	total, hidden, lines, dropped := 0, 0, run.Root.Lines, run.Root.Dropped
	var count func(s store.Span)
	count = func(s store.Span) {
		for _, c := range s.Children {
			total++
			if c.Hidden {
				hidden++
			}
			lines += c.Lines
			dropped += c.Dropped
			count(c)
		}
	}
	count(run.Root)
	add("Spans", fmt.Sprintf("%d below the command, %d of them hidden", total, hidden), false)
	add("Line events", fmt.Sprintf("%d, %d not sent to the sinks", lines, dropped), false)
	return f
}

// runStamp writes a time in full, in the local time zone; "" for none.
func runStamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04:05.000 MST")
}
