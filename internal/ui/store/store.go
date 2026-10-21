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
	// Statuses are keyed by realm and cluster, for the clusters whose page
	// is open.
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
	subs map[chan Topic]struct{}
}

// New returns an empty store.
func New() *Store {
	return &Store{subs: map[chan Topic]struct{}{}}
}

// Snapshot returns the current snapshot.
func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snap
}

// Subscribe returns a channel that receives the topic of each change, and
// a function that ends the subscription. A subscriber that falls behind
// misses notifications while it has some pending, and then renders from
// the snapshot it reads, which holds every change.
func (s *Store) Subscribe() (<-chan Topic, func()) {
	ch := make(chan Topic, 8)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}
}

// update changes the snapshot with fn under the lock and notifies the
// subscribers of each of ts.
func (s *Store) update(fn func(*Snapshot), ts ...Topic) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.snap)
	for _, t := range ts {
		s.notify(t)
	}
}

// notify tells the subscribers that t changed. The caller holds the lock.
func (s *Store) notify(t Topic) {
	for ch := range s.subs {
		select {
		case ch <- t:
		default:
			// The subscriber has notices pending and renders from the
			// snapshot it reads then, which includes this change.
		}
	}
}

// SetRuns replaces the runs and notifies TopicRuns and the topic of each
// run in changed.
func (s *Store) SetRuns(runs []Run, changed ...string) {
	ts := []Topic{TopicRuns}
	for _, id := range changed {
		ts = append(ts, RunTopic(id))
	}
	s.update(func(sn *Snapshot) { sn.Runs = runs }, ts...)
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
// is left alone.
func (s *Store) UpdateTask(id string, fn func(*Task)) {
	s.update(func(sn *Snapshot) {
		i := slices.IndexFunc(sn.Tasks, func(t Task) bool { return t.ID == id })
		if i < 0 {
			return
		}
		sn.Tasks = slices.Clone(sn.Tasks)
		fn(&sn.Tasks[i])
	}, TopicTasks)
}

// RemoveTask removes the task with the ID id from the tasks shown.
func (s *Store) RemoveTask(id string) {
	s.update(func(sn *Snapshot) {
		sn.Tasks = slices.DeleteFunc(slices.Clone(sn.Tasks), func(t Task) bool { return t.ID == id })
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
