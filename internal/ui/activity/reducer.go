// SPDX-License-Identifier: LGPL-3.0-or-later

// Package activity follows sind's event log, the go-clikit event log (v1)
// that every sind command appends to and that sind ui reads its progress
// display from, and reduces its lines to store.Run values: each run is one
// command with the tree of its spans.
//
// A Reducer turns lines into runs, a Tail reads the lines of a growing,
// rotating file, and Follow keeps a store.Store's runs up to date. The
// helpers in view.go answer the questions a page asks of a run.
package activity

import (
	"cmp"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

// logVersion is the version of the event log the reducer reads.
const logVersion = 1

// defaultMaxSpans bounds the spans of one run the reducer keeps; the spans
// that start after it are left out.
const defaultMaxSpans = 1 << 17

// Span kinds, as the event log writes them.
const (
	KindCommand = "command"
	KindStep    = "step"
	KindBatch   = "batch"
	KindTarget  = "target"
	KindCall    = "call"
	KindWait    = "wait"
)

// Span states, as the event log writes them.
const (
	StateQueued  = "queued"
	StateRunning = "running"
	StateEnded   = "ended"
)

// Event types, as the event log writes them.
const (
	typeTrace   = "trace"
	typeStart   = "start"
	typeRun     = "run"
	typeUpdate  = "update"
	typeAdvance = "advance"
	typeLine    = "line"
	typeEnd     = "end"
	typeSuspend = "suspend"
	typeResume  = "resume"
)

// logLine is what the reducer reads of a line of the event log: the trace
// line that begins a run, or an event of one of its spans. Keys it does not
// name are left out, as the log's versioning rules ask of a reader.
type logLine struct {
	V       int      `json:"v"`
	Type    string   `json:"type"`
	Run     string   `json:"run"`
	Trace   string   `json:"trace"`
	Program string   `json:"program"`
	Version string   `json:"version"`
	Time    string   `json:"time"`
	Span    string   `json:"span"`
	Parent  string   `json:"parent"`
	Kind    string   `json:"kind"`
	Name    string   `json:"name"`
	Flags   []string `json:"flags"`
	State   string   `json:"state"`
	Node    string   `json:"node"`
	Host    string   `json:"host"`
	Role    string   `json:"role"`
	Total   int      `json:"total"`
	Limit   int      `json:"limit"`
	Batch   string   `json:"batch"`
	Message string   `json:"message"`
	Timeout float64  `json:"timeout"`
	Exit    *int     `json:"exit"`
	Amount  int64    `json:"amount"`
	Size    int64    `json:"size"`
	Unit    string   `json:"unit"`
	Status  string   `json:"status"`
	Class   string   `json:"class"`
	Err     string   `json:"err"`
	Dropped int      `json:"dropped"`
}

// Reducer turns the lines of event logs into runs and their span trees.
// The lines of several runs may be interleaved, as when commands append to
// one file at once. A Reducer is not safe for concurrent use.
type Reducer struct {
	keep     int
	maxSpans int
	runs     map[string]*run
	// order lists the runs as their first line came, oldest first.
	order []*run
	// gone remembers the runs that were dropped, so that a late line of
	// one does not bring it back without its start.
	gone    map[string]struct{}
	changed map[string]int
	next    int
}

// run is a run as the reducer builds it; spans are the mutable spans of
// the run, without their children.
type run struct {
	id, trace, parent string
	program, version  string
	seq               int
	spans             map[string]*store.Span
	// started lists the spans in the order their first event came.
	started   []*store.Span
	root      *store.Span
	last      time.Time
	suspended bool
	// snap is the run as Runs returns it, valid while dirty is false.
	snap  store.Run
	dirty bool
}

// NewReducer returns an empty reducer that keeps at most keep runs, at
// least one: the newest, and of the older ones those that have not ended
// last.
func NewReducer(keep int) *Reducer {
	return &Reducer{
		keep:     max(keep, 1),
		maxSpans: defaultMaxSpans,
		runs:     map[string]*run{},
		gone:     map[string]struct{}{},
		changed:  map[string]int{},
	}
}

// Reset forgets every run, as when the log was rotated or truncated and is
// read again from its start. The IDs of the runs forgotten are reported by
// Changed.
func (r *Reducer) Reset() {
	for _, ru := range r.order {
		r.changed[ru.id] = ru.seq
	}
	clear(r.runs)
	clear(r.gone)
	r.order = nil
}

// Changed returns the IDs of the runs that changed, or were dropped, since
// the last call, oldest run first, and forgets them.
func (r *Reducer) Changed() []string {
	ids := slices.SortedFunc(maps.Keys(r.changed), func(a, b string) int {
		return cmp.Compare(r.changed[a], r.changed[b])
	})
	clear(r.changed)
	return ids
}

// Line reads one line of a log. A line that is not JSON, that is cut short,
// of another version of the format, of a type it does not know or without
// what its type needs, is left out, and so are the keys and values it does
// not know.
func (r *Reducer) Line(b []byte) {
	var l logLine
	if err := json.Unmarshal(b, &l); err != nil {
		// A value of the wrong type leaves its key out, and the rest of
		// the line stands.
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &typeErr) {
			return
		}
	}
	if l.V != logVersion || l.Run == "" {
		return
	}
	t, timeErr := time.Parse(time.RFC3339Nano, l.Time)
	switch l.Type {
	case typeTrace, typeSuspend, typeResume:
	case typeStart, typeRun, typeUpdate, typeAdvance, typeLine, typeEnd:
		if l.Span == "" || timeErr != nil {
			return
		}
	default:
		return
	}
	if _, dropped := r.gone[l.Run]; dropped {
		return
	}
	ru := r.runs[l.Run]
	if ru == nil {
		ru = r.newRun(l.Run)
	}
	ru.dirty = true
	r.changed[ru.id] = ru.seq
	if l.Trace != "" {
		ru.trace = l.Trace
	}
	if timeErr == nil && t.After(ru.last) {
		ru.last = t.UTC()
	}
	switch l.Type {
	case typeTrace:
		ru.parent = l.Parent
		ru.program, ru.version = clean(l.Program), clean(l.Version)
	case typeSuspend:
		ru.suspended = true
	case typeResume:
		ru.suspended = false
	default:
		ru.event(&l, t.UTC(), r.maxSpans)
	}
}

// newRun adds a run, and drops the runs beyond the limit.
func (r *Reducer) newRun(id string) *run {
	ru := &run{id: id, seq: r.next, spans: map[string]*store.Span{}}
	r.next++
	r.runs[id] = ru
	r.order = append(r.order, ru)
	r.trim()
	return ru
}

// trim drops the oldest runs beyond the limit, those that ended first.
func (r *Reducer) trim() {
	for len(r.order) > r.keep {
		i := slices.IndexFunc(r.order, func(ru *run) bool { return ru.root != nil && ru.root.Done() })
		if i < 0 {
			i = 0
		}
		ru := r.order[i]
		r.order = slices.Delete(r.order, i, i+1)
		delete(r.runs, ru.id)
		r.changed[ru.id] = ru.seq
		if len(r.gone) >= 16*r.keep {
			clear(r.gone)
		}
		r.gone[ru.id] = struct{}{}
	}
}

// event applies an event of a span.
func (ru *run) event(l *logLine, t time.Time, maxSpans int) {
	sp := ru.spans[l.Span]
	if sp == nil {
		if len(ru.spans) >= maxSpans {
			return
		}
		sp = &store.Span{
			ID: l.Span, Parent: l.Parent, State: StateRunning, Queued: t, Started: t,
			// The flags are the span's from its start.
			Hidden:    slices.Contains(l.Flags, "hidden"),
			Fold:      slices.Contains(l.Flags, "fold"),
			ShowLines: slices.Contains(l.Flags, "show-lines"),
			DryRun:    slices.Contains(l.Flags, "dry-run"),
		}
		if l.State == StateQueued {
			sp.State, sp.Started = StateQueued, time.Time{}
		}
		ru.spans[sp.ID] = sp
		ru.started = append(ru.started, sp)
		if ru.root == nil && l.Kind == KindCommand {
			ru.root = sp
		}
	}
	if sp.Done() {
		return
	}
	merge(sp, l)
	switch l.Type {
	case typeRun:
		if sp.State == StateQueued {
			sp.State, sp.Started = StateRunning, t
		}
	case typeLine:
		sp.Lines++
		sp.Dropped += max(l.Dropped, 0)
	case typeEnd:
		sp.State = StateEnded
		sp.Ended = &t
		sp.Status, sp.Class = known(l.Status, statuses), known(l.Class, classes)
		sp.Err = clean(l.Err)
		sp.Exit = l.Exit
		sp.Dropped = max(l.Dropped, 0)
	}
}

// statuses and classes are the values the log lists for status and class.
var (
	statuses = []string{store.StatusOK, store.StatusFailed, store.StatusCanceled, store.StatusSkipped}
	classes  = []string{"target", "transport", "timeout", "auth", "pin", "usage", "canceled"}
	kinds    = []string{KindCommand, KindStep, KindBatch, KindTarget, KindCall, KindWait}
	units    = []string{"items", "bytes", "percent"}
)

// known returns v when it is one of the values, and "" otherwise.
func known(v string, values []string) string {
	if slices.Contains(values, v) {
		return v
	}
	return ""
}

// merge takes into sp what an event says of the span, which every event of
// a span repeats.
func merge(sp *store.Span, l *logLine) {
	if k := known(l.Kind, kinds); k != "" {
		sp.Kind = k
	}
	if l.Name != "" {
		sp.Name = clean(l.Name)
	}
	if l.Node != "" {
		sp.Node = clean(l.Node)
	}
	if l.Host != "" {
		sp.Host = clean(l.Host)
	}
	if l.Role != "" {
		sp.Role = clean(l.Role)
	}
	// Total only ever grows.
	sp.Total = max(sp.Total, l.Total)
	if l.Limit > 0 {
		sp.Limit = l.Limit
	}
	if l.Batch != "" {
		sp.Batch = clean(l.Batch)
	}
	if l.Message != "" {
		sp.Message = clean(l.Message)
	}
	if l.Timeout > 0 {
		sp.Timeout = seconds(l.Timeout)
	}
	if u := known(l.Unit, units); u != "" {
		sp.Unit = u
	}
	if l.Size > 0 {
		sp.Size = l.Size
	}
	// An event carries the amount from the first that sets the work, and
	// leaves out an amount of 0, so that a missing one is 0 once the span
	// has work, and no work before.
	if sp.Unit != "" || sp.Size > 0 || l.Amount > 0 {
		sp.Amount = max(l.Amount, 0)
	}
}

// seconds converts the seconds of the log to a duration, bounded.
func seconds(s float64) time.Duration {
	return time.Duration(min(s, float64(math.MaxInt64/int64(time.Second))) * float64(time.Second))
}

// clean strips what acts on a terminal or a page from a text of the log:
// control characters, and the characters that reorder what is shown. Tabs
// and line breaks become spaces. clikit sanitises what it logs, and this
// covers a log that something else wrote.
func clean(s string) string {
	return strings.Map(func(c rune) rune {
		switch {
		case c == '\t' || c == '\n' || c == '\r':
			return ' '
		case unicode.IsControl(c), unicode.Is(unicode.Bidi_Control, c):
			return -1
		}
		return c
	}, s)
}

// Runs returns the runs that have a command span, newest first. They are
// copies: the reducer never changes them, nor they the reducer.
func (r *Reducer) Runs() []store.Run {
	var out []store.Run
	for _, ru := range slices.Backward(r.order) {
		if ru.root == nil {
			continue
		}
		if ru.dirty {
			ru.snap, ru.dirty = ru.build(), false
		}
		out = append(out, ru.snap)
	}
	return out
}

// build makes the Run of the spans seen: their tree by parent, children in
// the order they started, and the spans whose parent is not known below
// the root.
func (ru *run) build() store.Run {
	kids := make(map[string][]*store.Span, len(ru.started))
	for _, sp := range ru.started {
		if sp == ru.root {
			continue
		}
		parent := sp.Parent
		if _, known := ru.spans[parent]; !known || parent == sp.ID {
			parent = ru.root.ID
		}
		kids[parent] = append(kids[parent], sp)
	}
	root := tree(ru.root, kids)
	title := root.Name
	if root.Node != "" {
		title += " " + root.Node
	}
	// The pointers a span holds are set once, never written through.
	out := store.Run{
		ID: ru.id, Trace: ru.trace, Parent: ru.parent,
		Program: ru.program, Version: ru.version,
		Title: title, Start: root.Queued, End: root.Ended, Last: ru.last,
		Status: root.Status, Class: root.Class, Err: root.Err,
		Root: root,
	}
	out.Suspended = ru.suspended && !out.Done()
	return out
}

// tree returns a copy of sp with its descendants as children.
func tree(sp *store.Span, kids map[string][]*store.Span) store.Span {
	out := *sp
	for _, c := range kids[sp.ID] {
		out.Children = append(out.Children, tree(c, kids))
	}
	return out
}
