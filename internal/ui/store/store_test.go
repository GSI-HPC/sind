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
	ch, done := s.Subscribe()
	defer done()

	s.SetRuns([]Run{{ID: "b", Trace: "t2"}, {ID: "a", Trace: "t1"}}, "b")
	assert.Equal(t, TopicRuns, <-ch)
	assert.Equal(t, RunTopic("b"), <-ch)

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

func TestSlowSubscriberMissesOnlyPendingNotices(t *testing.T) {
	s := New()
	ch, done := s.Subscribe()
	for range 20 {
		s.AddTask(Task{})
	}
	assert.Len(t, ch, cap(ch))
	done()
	s.AddTask(Task{})
	assert.Len(t, s.Snapshot().Tasks, 21)
}

func TestSpanDone(t *testing.T) {
	now := time.Now()
	assert.False(t, Span{}.Done())
	assert.True(t, Span{Ended: &now}.Done())
}
