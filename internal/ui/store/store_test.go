// SPDX-License-Identifier: LGPL-3.0-or-later

package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuns(t *testing.T) {
	s := New()
	sub := s.Subscribe()
	defer sub.Close()

	s.SetRuns([]Run{{ID: "b", Trace: "t2"}, {ID: "a", Trace: "t1"}}, "b")
	assert.Equal(t, []Topic{TopicRuns, RunTopic("b")}, sub.Topics())

	sn := s.Snapshot()
	r, ok := sn.Run("a")
	require.True(t, ok)
	assert.Equal(t, "t1", r.Trace)
	_, ok = sn.Run("c")
	assert.False(t, ok)

	r, ok = sn.RunOfTrace("t2")
	require.True(t, ok)
	assert.Equal(t, "b", r.ID)
	_, ok = sn.RunOfTrace("")
	assert.False(t, ok)
	_, ok = sn.RunOfTrace("t3")
	assert.False(t, ok)
	assert.False(t, r.Done())
}

func TestTasks(t *testing.T) {
	s := New()
	s.AddTask(Task{ID: "1", Status: TaskRunning})
	s.AddTask(Task{ID: "2", Status: TaskRunning})
	before := s.Snapshot()

	now := time.Now()
	s.UpdateTask("1", func(t *Task) { t.Status, t.End = TaskOK, &now })
	s.UpdateTask("gone", func(*Task) { t.Fatal("called for a task that is gone") })

	task, ok := s.Snapshot().Task("1")
	require.True(t, ok)
	assert.True(t, task.Done())
	assert.Equal(t, TaskOK, task.Status)
	old, _ := before.Task("1")
	assert.False(t, old.Done(), "a published snapshot never changes")

	s.RemoveTask("1")
	_, ok = s.Snapshot().Task("1")
	assert.False(t, ok)
	assert.Len(t, s.Snapshot().Tasks, 1)
}

func TestSlowSubscriberLosesNoTopic(t *testing.T) {
	s := New()
	sub := s.Subscribe()
	for i := range 20 {
		s.AddTask(Task{})
		s.SetRuns(nil, string(rune('a'+i)))
	}
	// Each topic once, in the order of its first change, however many
	// changes there were and however few places a queue would have had.
	want := []Topic{TopicTasks, TopicRuns}
	for i := range 20 {
		want = append(want, RunTopic(string(rune('a'+i))))
	}
	assert.Len(t, sub.C(), 1, "one wake-up stands for them all")
	<-sub.C()
	assert.Equal(t, want, sub.Topics())
	assert.Empty(t, sub.Topics())
	assert.Empty(t, sub.C())

	// A change after the take wakes the subscriber again; after Close it
	// does not.
	s.SetRuns(nil, "x")
	<-sub.C()
	assert.Equal(t, []Topic{TopicRuns, RunTopic("x")}, sub.Topics())
	sub.Close()
	s.AddTask(Task{})
	assert.Empty(t, sub.C())
	assert.Len(t, s.Snapshot().Tasks, 21)
}

func TestSpanDone(t *testing.T) {
	now := time.Now()
	assert.False(t, Span{}.Done())
	assert.True(t, Span{Ended: &now}.Done())
}

func TestRunsEndWithTheirTask(t *testing.T) {
	s := New()
	start := time.Now()
	end := start.Add(time.Minute)
	s.AddTask(Task{ID: "t1", Trace: "tr1", Status: TaskRunning})
	s.AddTask(Task{ID: "t2", Trace: "tr2", Status: TaskRunning})
	s.AddTask(Task{ID: "t3", Trace: "tr3", Status: TaskRunning})
	s.AddTask(Task{ID: "t4", Status: TaskRunning})
	s.SetRuns([]Run{
		{ID: "a", Trace: "tr1", Start: start},
		{ID: "b", Trace: "tr2", Start: start},
		{ID: "c", Trace: "tr3", Start: start, End: &start, Status: StatusOK},
		{ID: "d", Start: start},
	})
	sub := s.Subscribe()
	defer sub.Close()

	// The task of a ends, killed: its run, which the log never ended, ends.
	s.UpdateTask("t1", func(t *Task) { t.End, t.Status, t.Err = &end, TaskFailed, "sind signal: killed" })
	assert.Equal(t, []Topic{TopicTasks, RunTopic("a"), TopicRuns}, sub.Topics())
	a, _ := s.Snapshot().Run("a")
	require.True(t, a.Done())
	assert.True(t, a.Unfinished)
	assert.Equal(t, end, *a.End)
	assert.Equal(t, StatusFailed, a.Status)
	assert.Equal(t, "sind signal: killed", a.Err)

	// A task whose run ended in the log changes no run; nor does a task
	// without a trace, a task that runs on, or one that is gone.
	s.UpdateTask("t3", func(t *Task) { t.End, t.Status = &end, TaskOK })
	s.UpdateTask("t4", func(t *Task) { t.End, t.Status = &end, TaskOK })
	s.UpdateTask("t2", func(t *Task) { t.Canceling = true })
	s.UpdateTask("gone", func(t *Task) { t.End = &end })
	assert.Equal(t, []Topic{TopicTasks}, sub.Topics())
	c, _ := s.Snapshot().Run("c")
	assert.False(t, c.Unfinished)

	// The task of b ends canceled; its run is set again by the log
	// without an end, and still ends with its task, also when the task
	// was dismissed.
	s.UpdateTask("t2", func(t *Task) { t.End, t.Status = &end, TaskCanceled })
	s.RemoveTask("t2")
	s.SetRuns([]Run{{ID: "b", Trace: "tr2", Start: start}, {ID: "x", Trace: "tr1", Start: start}})
	b, _ := s.Snapshot().Run("b")
	assert.Equal(t, StatusCanceled, b.Status)
	x, _ := s.Snapshot().Run("x")
	assert.Equal(t, StatusFailed, x.Status, "every run of the trace")

	// A task that succeeds ends its run ok.
	s.AddTask(Task{ID: "t5", Trace: "tr5"})
	s.SetRuns([]Run{{ID: "e", Trace: "tr5", Start: start, Err: "own"}})
	s.UpdateTask("t5", func(t *Task) { t.End, t.Status = &end, TaskOK })
	e, _ := s.Snapshot().Run("e")
	assert.Equal(t, StatusOK, e.Status)
}
