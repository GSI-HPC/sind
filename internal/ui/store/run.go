// SPDX-License-Identifier: LGPL-3.0-or-later

package store

import "time"

// Span statuses, as the event log writes them; "" while a span runs.
const (
	StatusOK       = "ok"
	StatusFailed   = "failed"
	StatusCanceled = "canceled"
	StatusSkipped  = "skipped"
)

// Run is one sind command, as its event log tells it.
type Run struct {
	// ID is the log's run id, 16 hexadecimal digits.
	ID string
	// Trace is the trace the run belongs to: the one a caller handed on
	// in TRACEPARENT, which is how sind ui finds the run of a task it
	// started.
	Trace string
	// Parent is the caller's span the run works under, from TRACEPARENT.
	Parent  string
	Program string
	Version string
	// Title is the command as a user would read it, such as
	// "create cluster dev": the name of the root span.
	Title string
	Start time.Time
	// End is when the root span ended, nil while the run goes on.
	End *time.Time
	// Last is the time of the newest event of the run.
	Last time.Time
	// Status, Class and Err are the root span's.
	Status string
	Class  string
	Err    string
	// Suspended is set between a suspend and a resume event.
	Suspended bool
	// Unfinished is set for a run whose end the log lacks, as its process
	// was killed. End is then when the task that started it ended, with the
	// task's Status, or the time of its newest event once that is old
	// (activity.StaleAfter), with no Status.
	Unfinished bool
	// Root is the command span and the spans below it.
	Root Span
}

// Done reports whether the run has ended.
func (r Run) Done() bool { return r.End != nil }

// Span is one span of a run and the spans started under it, in the order
// they started.
type Span struct {
	ID     string
	Parent string
	// Kind is command, step, batch, target, call or wait.
	Kind string
	Name string
	// Hidden, Fold, ShowLines and DryRun are the span's flags.
	Hidden    bool
	Fold      bool
	ShowLines bool
	DryRun    bool
	// State is queued, running or ended.
	State string
	// Node, Host and Role say what a target or a call is for.
	Node string
	Host string
	Role string
	// Total, Limit and Batch are a step's or a batch's counts: the
	// targets it expects, how many run at once, and a batch's place
	// such as "2/5".
	Total int
	Limit int
	Batch string
	// Message is the span's latest one-line message.
	Message string
	// Timeout is the bound of a call or the length of a wait.
	Timeout time.Duration
	// Amount and Size are the span's own work done and to do, in Unit:
	// items, bytes or percent. Size 0 is not known.
	Amount int64
	Size   int64
	Unit   string
	// Status, Class, Err and Exit are how the span ended; Exit is nil
	// when the span gave no exit code.
	Status string
	Class  string
	Err    string
	Exit   *int
	// Queued is the time of the span's start event; Started the time it
	// began to run (equal to Queued for a span started running); Ended
	// nil until its end event.
	Queued  time.Time
	Started time.Time
	Ended   *time.Time
	// Lines counts the span's line events (the log holds no text);
	// Dropped the lines the sinks were not sent.
	Lines   int
	Dropped int
	// Children are the spans started under this one.
	Children []Span
}

// Done reports whether the span has ended.
func (s Span) Done() bool { return s.Ended != nil }

// Task states.
const (
	TaskRunning  = "running"
	TaskOK       = "ok"
	TaskFailed   = "failed"
	TaskCanceled = "canceled"
)

// Task is a command that sind ui started for a user. The steps it shows
// come from the run in the event log with the same Trace.
type Task struct {
	ID string
	// Action is the id of the action that built the command.
	Action string
	// Display is the command as the dialog showed it, quoted for a shell.
	Display string
	// Realm and Cluster name what the command works on, empty for none.
	Realm   string
	Cluster string
	// Trace is the trace ID that the command got in TRACEPARENT.
	Trace string
	Start time.Time
	End   *time.Time
	// Status is TaskRunning, TaskOK, TaskFailed or TaskCanceled.
	Status string
	// Canceling is set once the user asked to stop the command.
	Canceling bool
	// Exit is the child's exit code once it ended.
	Exit int
	// Err says in one line why a failed command failed.
	Err string
	// Output is the end of what the command wrote to standard error,
	// escaped for display.
	Output string
}

// Done reports whether the command has ended.
func (t Task) Done() bool { return t.End != nil }
