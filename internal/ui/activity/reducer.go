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
// enumerations are the log's words. A Reducer is not safe for concurrent
// use.
type Reducer struct {
	*eventlog.Reducer
}

// NewReducer returns an empty reducer that keeps at most keep runs, at
// least one: the newest, and of the older ones those that have not ended.
func NewReducer(keep int) *Reducer {
	return &Reducer{eventlog.NewReducer(eventlog.Options{Keep: max(keep, 1)})}
}

// Runs returns the runs that have a command span, newest first.
func (r *Reducer) Runs() []store.Run {
	runs := r.Reducer.Runs()
	out := make([]store.Run, len(runs))
	for i, ru := range runs {
		out[i] = convertRun(ru)
	}
	return out
}

// convertRun returns the run as sind ui holds it.
func convertRun(ru eventlog.Run) store.Run {
	root := convertSpan(ru.Root)
	title := root.Name
	if root.Node != "" {
		title += " " + root.Node
	}
	out := store.Run{
		ID: ru.ID, Trace: ru.Trace.Trace.String(), Parent: spanID(ru.Trace.Parent),
		Program: ru.Program, Version: ru.Version,
		Title: title, Start: root.Queued, End: root.Ended, Last: ru.Last,
		Status: root.Status, Class: root.Class, Err: root.Err,
		Root: root,
	}
	out.Suspended = ru.Suspended && !out.Done()
	return out
}

// convertSpan returns the span and its descendants as sind ui holds them.
func convertSpan(s eventlog.Span) store.Span {
	out := store.Span{
		ID: spanID(s.ID), Parent: spanID(s.Parent),
		Name:      s.Name,
		Hidden:    s.Flags&progress.Hidden != 0,
		Fold:      s.Flags&progress.Fold != 0,
		ShowLines: s.Flags&progress.ShowLines != 0,
		DryRun:    s.Flags&progress.DryRun != 0,
		Node:      s.Node, Host: s.Host, Role: s.Role,
		Total: s.Total, Limit: s.Limit, Batch: s.Batch, Message: s.Message,
		Timeout: s.Timeout, Amount: s.Amount, Size: s.Size,
		Exit:    s.Exit,
		Err:     s.Err,
		Queued:  s.Queued,
		Started: s.Started,
		Lines:   s.Lines, Dropped: s.Dropped,
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
		out.Children = append(out.Children, convertSpan(c))
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
