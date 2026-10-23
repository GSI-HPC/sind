// SPDX-License-Identifier: LGPL-3.0-or-later

// Package store holds what sind ui shows, in memory: the collectors write
// it, the pages and the live updates read it.
//
// A Snapshot is never changed once it is published. Each Set replaces the
// part it sets with a new value and notifies the subscribers of that part's
// topic, so a reader may keep a Snapshot as long as it likes.
package store

import (
	"slices"
	"sync"

	"github.com/GSI-HPC/go-clikit/localweb/sse"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/state"
)

// Topic names a part of the snapshot that changes as a whole. Some topics
// name one item, such as RunTopic("0123456789abcdef").
type Topic string

// The topics of a Snapshot.
const (
	// TopicRuns changes when a run of the event log starts, changes or
	// ends.
	TopicRuns Topic = "runs"
	// TopicTasks changes when a task starts, ends or is dismissed.
	TopicTasks Topic = "tasks"
)

// RunTopic is the topic of one run, which changes with every event of it,
// next to TopicRuns.
func RunTopic(id string) Topic { return Topic("run/" + id) }

// Snapshot is everything sind ui knows at one moment.
type Snapshot struct {
	// Runs are the runs of the event log, newest first.
	Runs []Run
	// Tasks are the commands sind ui started, oldest first.
	Tasks []Task

	// The sind data. Maps are replaced, never changed, by the setters in
	// sind.go; the keys are realm names unless noted.

	// Realms are sorted by name.
	Realms   []cluster.RealmSummary
	Clusters map[string][]cluster.Summary
	Nodes    map[string][]cluster.NodeSummary
	Networks map[string][]cluster.NetworkSummary
	Volumes  map[string][]cluster.VolumeSummary
	Meshes   map[string]Mesh
	// Statuses are keyed by realm and cluster, for the clusters listed
	// and the ones whose page is open.
	Statuses    map[StatusKey]*cluster.Status
	Locks       map[string]*state.LockHolder
	Monitorings map[string]*cluster.Monitoring
	// Doctor is the last host check, nil before the first.
	Doctor *DoctorReport
	Docker DockerState
}

// Store holds the current Snapshot and notifies subscribers of changes.
type Store struct {
	mu   sync.Mutex
	snap Snapshot
	// changes tells the subscribers which topics changed.
	changes sse.Notifier[Topic]
	// logRuns are the runs as the log tells them, before ended.
	logRuns []Run
	// ended holds the tasks that have ended, by their trace, so that their
	// runs end with them even when the log has no end.
	ended map[string]Task
}

// MaxEndedTasks is how many tasks that have ended the store keeps, the
// newest by start; an older one is dropped once another ends, and its run
// shows without its command.
const MaxEndedTasks = 25

// New returns an empty store.
func New() *Store {
	return &Store{ended: map[string]Task{}}
}

// Snapshot returns the current snapshot.
func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snap
}

// Notifier returns what tells of the topics that change, for a live update
// stream to subscribe to.
func (s *Store) Notifier() *sse.Notifier[Topic] { return &s.changes }

// Subscribe returns a subscription to the topics that change from now on.
// A subscriber that falls behind loses none: it takes the topics that
// changed since it last took them, each once, and then reads the snapshot,
// which holds every change. Close ends the subscription.
func (s *Store) Subscribe() *sse.Subscription[Topic] { return s.changes.Subscribe() }

// update changes the snapshot with fn under the lock and notifies the
// subscribers of each of ts.
func (s *Store) update(fn func(*Snapshot), ts ...Topic) {
	s.updateTopics(func(sn *Snapshot) []Topic { fn(sn); return ts })
}

// updateTopics changes the snapshot with fn under the lock and notifies the
// subscribers of each topic that fn returns.
func (s *Store) updateTopics(fn func(*Snapshot) []Topic) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.changes.Notify(fn(&s.snap)...)
}

// SetRuns replaces the runs and notifies TopicRuns and the topic of each
// run in changed. A run that has not ended, of a task that has, ends as the
// task did.
func (s *Store) SetRuns(runs []Run, changed ...string) {
	ts := []Topic{TopicRuns}
	for _, id := range changed {
		ts = append(ts, RunTopic(id))
	}
	s.update(func(sn *Snapshot) {
		s.logRuns = runs
		sn.Runs = s.endRuns(runs)
	}, ts...)
}

// endRuns returns runs with each run that has not ended, of a task that
// has, ended as the task did. The log lacks the end of such a run when its
// process was killed.
func (s *Store) endRuns(runs []Run) []Run {
	var out []Run
	for i, r := range runs {
		t, ok := s.ended[r.Trace]
		if r.Done() || r.Trace == "" || !ok {
			continue
		}
		if out == nil {
			out = slices.Clone(runs)
		}
		out[i] = endedAs(r, t)
	}
	if out == nil {
		return runs
	}
	return out
}

// endedAs returns r ended as the task t did.
func endedAs(r Run, t Task) Run {
	r.End, r.Unfinished = t.End, true
	switch t.Status {
	case TaskOK:
		r.Status = StatusOK
	case TaskCanceled:
		r.Status = StatusCanceled
	default:
		r.Status = StatusFailed
		if r.Err == "" {
			r.Err = t.Err
		}
	}
	return r
}

// Run returns the run with the ID id.
func (sn Snapshot) Run(id string) (Run, bool) {
	i := slices.IndexFunc(sn.Runs, func(r Run) bool { return r.ID == id })
	if i < 0 {
		return Run{}, false
	}
	return sn.Runs[i], true
}

// RunOfTrace returns the newest run of the trace trace.
func (sn Snapshot) RunOfTrace(trace string) (Run, bool) {
	i := slices.IndexFunc(sn.Runs, func(r Run) bool { return r.Trace == trace })
	if trace == "" || i < 0 {
		return Run{}, false
	}
	return sn.Runs[i], true
}

// AddTask adds a task to the tasks shown.
func (s *Store) AddTask(t Task) {
	s.update(func(sn *Snapshot) { sn.Tasks = append(slices.Clone(sn.Tasks), t) }, TopicTasks)
}

// UpdateTask changes the task with the ID id with fn. A task that is gone
// is left alone. When the task ends, the runs of its trace that the log has
// not ended end with it, and the tasks that ended before the newest
// MaxEndedTasks are dropped; the topics of the runs of both are notified.
func (s *Store) UpdateTask(id string, fn func(*Task)) {
	s.updateTopics(func(sn *Snapshot) []Topic {
		i := slices.IndexFunc(sn.Tasks, func(t Task) bool { return t.ID == id })
		if i < 0 {
			return []Topic{TopicTasks}
		}
		sn.Tasks = slices.Clone(sn.Tasks)
		fn(&sn.Tasks[i])
		t := sn.Tasks[i]
		if !t.Done() {
			return []Topic{TopicTasks}
		}
		ts := []Topic{TopicTasks}
		var dropped []string
		sn.Tasks, dropped = pruneTasks(sn.Tasks)
		for _, r := range sn.Runs {
			if r.Trace != "" && slices.Contains(dropped, r.Trace) {
				ts = append(ts, RunTopic(r.ID))
			}
		}
		if t.Trace == "" {
			return ts
		}
		s.ended[t.Trace] = Task{End: t.End, Status: t.Status, Err: t.Err}
		ended := false
		for _, r := range s.logRuns {
			if r.Trace == t.Trace && !r.Done() {
				ts, ended = append(ts, RunTopic(r.ID)), true
			}
		}
		if !ended {
			return ts
		}
		sn.Runs = s.endRuns(s.logRuns)
		return append(ts, TopicRuns)
	})
}

// pruneTasks drops from tasks, which it may change, the tasks that ended
// before the newest MaxEndedTasks of them, and returns the traces of those
// it dropped.
func pruneTasks(tasks []Task) ([]Task, []string) {
	var dropped []string
	n := 0
	for i := len(tasks) - 1; i >= 0; i-- {
		if !tasks[i].Done() {
			continue
		}
		if n++; n > MaxEndedTasks {
			dropped = append(dropped, tasks[i].Trace)
			tasks = slices.Delete(tasks, i, i+1)
		}
	}
	return tasks, dropped
}

// HideTasks hides the cards of the tasks that hide reports true of, as the
// user closed them: the tasks stay, for their run pages.
func (s *Store) HideTasks(hide func(Task) bool) {
	s.update(func(sn *Snapshot) {
		sn.Tasks = slices.Clone(sn.Tasks)
		for i := range sn.Tasks {
			if hide(sn.Tasks[i]) {
				sn.Tasks[i].Hidden = true
			}
		}
	}, TopicTasks)
}

// Task returns the task with the ID id.
func (sn Snapshot) Task(id string) (Task, bool) {
	i := slices.IndexFunc(sn.Tasks, func(t Task) bool { return t.ID == id })
	if i < 0 {
		return Task{}, false
	}
	return sn.Tasks[i], true
}
