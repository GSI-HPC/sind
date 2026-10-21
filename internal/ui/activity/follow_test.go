// SPDX-License-Identifier: LGPL-3.0-or-later

package activity

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

// runFollow starts follow in the background and returns a function that
// stops it and waits for it.
func runFollow(t *testing.T, st *store.Store, path string, active, idle time.Duration) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		follow(ctx, st, path, 10, active, idle)
	}()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Follow did not return")
		}
	}
}

// next returns the next notification, or fails after a while.
func next(t *testing.T, ch <-chan store.Topic) store.Topic {
	t.Helper()
	select {
	case topic := <-ch:
		return topic
	case <-time.After(5 * time.Second):
		t.Fatal("no notification")
		return ""
	}
}

// quiet fails when a notification comes within d.
func quiet(t *testing.T, ch <-chan store.Topic, d time.Duration) {
	t.Helper()
	select {
	case topic := <-ch:
		t.Fatalf("unexpected notification %q", topic)
	case <-time.After(d):
	}
}

func TestFollowSetsTheChangedRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.jsonl")
	st := store.New()
	ch, unsubscribe := st.Subscribe()
	defer unsubscribe()

	// A log that is not there is empty: nothing to set.
	stop := runFollow(t, st, path, 5*time.Millisecond, 5*time.Millisecond)
	defer stop()
	quiet(t, ch, 60*time.Millisecond)

	appendTo(t, path, logLines(runA, "one"))
	got := map[store.Topic]bool{}
	for len(got) < 2 {
		got[next(t, ch)] = true
	}
	assert.True(t, got[store.TopicRuns])
	assert.True(t, got[store.RunTopic(runA)])
	run, ok := st.Snapshot().Run(runA)
	require.True(t, ok)
	assert.Equal(t, "one", run.Title)

	// Lines that change no run are no change.
	appendTo(t, path, []byte("not json\n{\"v\":9}\n"))
	quiet(t, ch, 60*time.Millisecond)

	// Only the run that changed is named.
	appendTo(t, path, logLines("bbbbbbbbbbbbbbbb", "two"))
	got = map[store.Topic]bool{}
	for len(got) < 2 {
		got[next(t, ch)] = true
	}
	assert.True(t, got[store.RunTopic("bbbbbbbbbbbbbbbb")])
	assert.False(t, got[store.RunTopic(runA)])
	assert.Len(t, st.Snapshot().Runs, 2)
}

func TestFollowReadsLessOftenWhenNoRunGoesOn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.jsonl")
	// A run that has not ended.
	appendTo(t, path, line("trace", runA, `"trace":"t"`))
	appendTo(t, path, []byte("\n"))
	appendTo(t, path, ev("start", runA, "2026-01-01T00:00:00Z", `"span":"r","kind":"command","name":"one"`))
	appendTo(t, path, []byte("\n"))
	st := store.New()
	ch, unsubscribe := st.Subscribe()
	defer unsubscribe()
	// The idle interval is an hour, so only the active one reads again.
	stop := runFollow(t, st, path, 5*time.Millisecond, time.Hour)
	defer stop()
	next(t, ch)
	next(t, ch)
	r, _ := st.Snapshot().Run(runA)
	require.False(t, r.Done())

	appendTo(t, path, append(ev("end", runA, "2026-01-01T00:00:01Z", `"span":"r","status":"ok"`), '\n'))
	next(t, ch)
	next(t, ch)
	r, _ = st.Snapshot().Run(runA)
	require.True(t, r.Done())

	// Nothing goes on now: the log is not read for an hour.
	appendTo(t, path, logLines("bbbbbbbbbbbbbbbb", "two"))
	quiet(t, ch, 100*time.Millisecond)
	assert.Len(t, st.Snapshot().Runs, 1)
}

func TestFollow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.jsonl")
	appendTo(t, path, logLines(runA, "one"))
	st := store.New()
	ch, unsubscribe := st.Subscribe()
	defer unsubscribe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Follow(ctx, st, path)
	}()
	next(t, ch)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Follow did not return")
	}
	assert.Len(t, st.Snapshot().Runs, 1)
}

func TestAnyRunning(t *testing.T) {
	end := time.Now()
	assert.False(t, anyRunning(nil))
	assert.False(t, anyRunning([]store.Run{{End: &end}, {End: &end}}))
	assert.True(t, anyRunning([]store.Run{{End: &end}, {}}))
}
