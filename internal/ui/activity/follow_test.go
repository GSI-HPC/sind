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
func runFollow(t *testing.T, st *store.Store, path string, active, idle, staleAfter time.Duration) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		follow(ctx, st, path, 10, active, idle, staleAfter)
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

// forever is a time after which no run of the tests is stale.
const forever = 1 << 62

// notices returns a channel of the topics that the store notifies, one by
// one, until the test ends.
func notices(t *testing.T, st *store.Store) <-chan store.Topic {
	t.Helper()
	sub := st.Notifier().Subscribe()
	out := make(chan store.Topic, 64)
	done := make(chan struct{})
	t.Cleanup(func() { sub.Close(); close(done) })
	go func() {
		for {
			select {
			case <-sub.Changed():
				for _, topic := range sub.Take() {
					out <- topic
				}
			case <-done:
				return
			}
		}
	}()
	return out
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
	ch := notices(t, st)

	// A log that is not there is empty: nothing to set.
	stop := runFollow(t, st, path, 5*time.Millisecond, 5*time.Millisecond, forever)
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
	ch := notices(t, st)
	// The idle interval is an hour, so only the active one reads again.
	stop := runFollow(t, st, path, 5*time.Millisecond, time.Hour, forever)
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
	ch := notices(t, st)
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

func TestFollowEndsAStaleRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.jsonl")
	// A run that has not ended, its last event long ago.
	appendTo(t, path, line("trace", runA, `"trace":"t"`))
	appendTo(t, path, []byte("\n"))
	appendTo(t, path, ev("start", runA, "2026-01-01T00:00:00Z", `"span":"r","kind":"command","name":"one"`))
	appendTo(t, path, []byte("\n"))
	appendTo(t, path, logLines("bbbbbbbbbbbbbbbb", "two"))
	st := store.New()
	ch := notices(t, st)
	stop := runFollow(t, st, path, 5*time.Millisecond, time.Hour, time.Hour)
	defer stop()
	got := map[store.Topic]bool{}
	for len(got) < 3 {
		got[next(t, ch)] = true
	}
	r, _ := st.Snapshot().Run(runA)
	require.True(t, r.Done(), "ended as stale")
	assert.True(t, r.Unfinished)
	assert.Equal(t, r.Last, *r.End)
	assert.Empty(t, r.Status)
	other, _ := st.Snapshot().Run("bbbbbbbbbbbbbbbb")
	assert.False(t, other.Unfinished)
	// Nothing goes on now: the log is read once an hour, and the stale run
	// is not set again.
	quiet(t, ch, 60*time.Millisecond)
}

func TestEndStale(t *testing.T) {
	now := time.Now()
	end := now
	runs := []store.Run{{ID: "a", Last: now.Add(-2 * time.Hour)}, {ID: "b", Last: now}, {ID: "c", Last: now.Add(-3 * time.Hour), End: &end}}
	got, ids := endStale(runs, now.Add(-time.Hour))
	assert.Equal(t, []string{"a"}, ids)
	assert.True(t, got[0].Unfinished)
	assert.False(t, runs[0].Unfinished, "a copy")
	assert.False(t, got[1].Done())
	assert.Same(t, &end, got[2].End)

	same, ids := endStale(runs[1:], now.Add(-time.Hour))
	assert.Empty(t, ids)
	assert.Equal(t, runs[1:], same)
}

func TestFollowEndsARunThatFallsSilent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.jsonl")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	appendTo(t, path, line("trace", runA, `"trace":"t"`))
	appendTo(t, path, []byte("\n"))
	appendTo(t, path, ev("start", runA, now, `"span":"r","kind":"command","name":"one"`))
	appendTo(t, path, []byte("\n"))
	st := store.New()
	ch := notices(t, st)
	stop := runFollow(t, st, path, 5*time.Millisecond, 5*time.Millisecond, 300*time.Millisecond)
	defer stop()
	next(t, ch)
	next(t, ch)
	r, _ := st.Snapshot().Run(runA)
	require.False(t, r.Done(), "it runs")

	// No event comes: the run ends as stale, with no line read.
	assert.Equal(t, store.TopicRuns, next(t, ch))
	assert.Equal(t, store.RunTopic(runA), next(t, ch))
	r, _ = st.Snapshot().Run(runA)
	assert.True(t, r.Unfinished)
}
