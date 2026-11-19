// SPDX-License-Identifier: LGPL-3.0-or-later

// Package activity follows sind's event log, the go-clikit event log (v1)
// that every sind command appends to and that sind ui reads its progress
// display from, and reduces its lines to store.Run values: each run is one
// command with the tree of its spans.
//
// A Reducer turns lines into runs, with go-clikit's eventlog.Reducer, and
// Follow keeps a store.Store's runs up to date, reading the file with an
// eventlog.Tail. The helpers in view.go answer the questions a page asks
// of a run.
package activity

import (
	"bytes"
	"slices"
	"time"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/go-clikit/progress/eventlog"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

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

// Reducer turns the lines of event logs into runs and their span trees. It
// is eventlog.Reducer with the runs as store.Run values, whose texts and
// enumerations are the log's words, and the rate of each span that works
// in bytes, and the history of the amount of each span that works in a
// unit. A Reducer is not safe for concurrent use.
type Reducer struct {
	*eventlog.Reducer
	rates   rates
	history history
	// converted holds the runs as Runs last returned them, by ID.
	converted map[string]converted
}

// converted is a run as Runs converted it, with what tells whether
// eventlog.Reducer has built its run anew since: the newest event, and the
// first child of its command span. eventlog.Reducer builds a run that
// changes anew, every child of its command in a new slice, and returns
// the same run while it does not change.
type converted struct {
	run   store.Run
	last  time.Time
	first *eventlog.Span
}

// NewReducer returns an empty reducer that keeps at most keep runs, at
// least one: the newest, and of the older ones those that have not ended.
func NewReducer(keep int) *Reducer {
	return &Reducer{Reducer: eventlog.NewReducer(eventlog.ReducerOptions{KeepRuns: max(keep, 1)}),
		rates: rates{}, history: history{}, converted: map[string]converted{}}
}

// Take folds one line of a log in, as eventlog.Reducer does, and notes the
// amount of a span that works in bytes, for its rate, and of one that works
// in a unit, for its history. The rates and the histories read a line of
// a unit, decoded once for both.
func (r *Reducer) Take(b []byte) {
	r.Reducer.Take(b)
	if !bytes.Contains(b, []byte(`"unit"`)) {
		return
	}
	l, err := eventlog.Decode(b)
	if err != nil {
		return
	}
	r.rates.take(l)
	r.history.take(l)
}

// Reset forgets the runs, the rates and the histories, as the log is read again.
func (r *Reducer) Reset() {
	r.Reducer.Reset()
	clear(r.rates)
	clear(r.history)
	clear(r.converted)
}

// Runs returns the runs that have a command span, newest first. It
// converts only the runs that changed since the last call: while a run
// does not change, Runs returns the same store.Run again, its spans
// shared, so a caller does not change what it was given.
func (r *Reducer) Runs() []store.Run {
	runs := r.Reducer.Runs()
	out := make([]store.Run, len(runs))
	keep := make(map[string]bool, len(runs))
	next := make(map[string]converted, len(runs))
	for i, ru := range runs {
		c, ok := r.converted[ru.ID]
		if !ok || !c.of(ru) {
			c = converted{run: convertRun(ru, r.rates, r.history), last: ru.Last}
			if len(ru.Root.Children) > 0 {
				c.first = &ru.Root.Children[0]
			}
		}
		out[i] = c.run
		next[ru.ID] = c
		keep[ru.ID] = true
	}
	r.converted = next
	r.rates.forget(keep)
	r.history.forget(keep)
	return out
}

// of reports whether c is the conversion of ru: of the same build of the
// run. A run whose command has no children is converted each time, as it
// is cheap to.
func (c converted) of(ru eventlog.Run) bool {
	return c.first != nil && len(ru.Root.Children) > 0 && c.first == &ru.Root.Children[0] && c.last.Equal(ru.Last)
}

// convertRun returns the run as sind ui holds it.
func convertRun(ru eventlog.Run, rt rates, h history) store.Run {
	root := convertSpan(ru.Root, func(id progress.SpanID) (float64, []store.Sample) {
		return rt.rate(ru.ID, id, ru.Last), h[spanKey{ru.ID, id}]
	})
	command := root.Name
	if root.Node != "" {
		command += " " + root.Node
	}
	// eventlog clears Suspended once the run has ended.
	return store.Run{
		ID: ru.ID, Trace: ru.Trace.ID.String(), Parent: spanID(ru.Trace.Parent),
		FromUI:  store.IsUITraceState(ru.Trace.State),
		Program: ru.Program, Version: ru.Version,
		Title: title(root), Command: command, Start: root.Queued, End: root.Ended, Last: ru.Last,
		Status: root.Status, Class: root.Class, Err: root.Err,
		Suspended: ru.Suspended, Root: root,
	}
}

// convertSpan returns the span and its descendants as sind ui holds them,
// with the rate and the samples of each that work gives; the samples are a
// copy, which the reducer does not change.
func convertSpan(s eventlog.Span, work func(progress.SpanID) (float64, []store.Sample)) store.Span {
	rate, samples := work(s.ID)
	out := store.Span{
		ID: spanID(s.ID), Parent: spanID(s.Parent),
		Name:   s.Name,
		Hidden: s.Flags&progress.FlagHidden != 0,
		Node:   s.Node, Role: s.Role,
		Total: s.Total, Batch: s.Batch, Message: s.Message,
		Timeout: s.Timeout, Amount: s.Amount, Size: s.Size,
		Err:     s.Err,
		Rate:    rate,
		Samples: slices.Clone(samples),
		Queued:  s.Queued,
		Started: s.Started,
		Lines:   s.Lines, Dropped: s.Dropped,
	}
	if s.HasExit {
		exit := s.Exit
		out.Exit = &exit
	}
	if s.Kind != 0 {
		out.Kind = s.Kind.String()
	}
	if s.State != 0 {
		out.State = s.State.String()
	}
	if s.Unit != 0 {
		out.Unit = s.Unit.String()
	}
	if s.Status != 0 {
		out.Status = s.Status.String()
	}
	if s.Class != progress.ClassNone {
		out.Class = s.Class.String()
	}
	if !s.Ended.IsZero() {
		ended := s.Ended
		out.Ended = &ended
	}
	for _, c := range s.Children {
		out.Children = append(out.Children, convertSpan(c, work))
	}
	return out
}

// spanID returns the ID of a span as the log writes it, "" for none.
func spanID(id progress.SpanID) string {
	if id == 0 {
		return ""
	}
	return id.String()
}
