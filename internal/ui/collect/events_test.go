// SPDX-License-Identifier: LGPL-3.0-or-later

package collect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GSI-HPC/go-clikit/localweb/sse"
	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventsArgs(t *testing.T) {
	assert.Equal(t, []string{
		"events", "--filter", "label=sind.realm", "--filter", "type=container", "--filter", "type=network",
		"--format", "{{json .}}",
	}, EventsArgs())
}

func line(typ, action string, attrs map[string]string) string {
	var b strings.Builder
	for k, v := range attrs {
		fmt.Fprintf(&b, "%q:%q,", k, v)
	}
	return fmt.Sprintf(`{"Type":%q,"Action":%q,"Actor":{"ID":"x","Attributes":{%s"image":"img"}},"time":1}`+"\n", typ, action, b.String())
}

func TestParseEvent(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
		want Event
		ok   bool
	}{
		{"container start", line("container", "start", map[string]string{"sind.realm": "sind", "sind.cluster": "dev"}),
			Event{Kind: KindChange, Realm: "sind", Cluster: "dev"}, true},
		{"mesh container", line("container", "die", map[string]string{"sind.realm": "sind"}),
			Event{Kind: KindChange, Realm: "sind"}, true},
		{"cluster network", line("network", "connect", map[string]string{"sind.realm": "sind", "sind.cluster": "dev", "name": "sind-dev-net"}),
			Event{Kind: KindChange, Realm: "sind", Cluster: "dev"}, true},
		{"lock", line("network", "create", map[string]string{"sind.realm": "ci", "name": "ci-lock", "sind.cluster": "x"}),
			Event{Kind: KindLock, Realm: "ci"}, true},
		{"exec", line("container", "exec_create: sh", map[string]string{"sind.realm": "sind"}), Event{}, false},
		{"no realm", line("container", "start", nil), Event{}, false},
		{"other type", line("volume", "create", map[string]string{"sind.realm": "sind"}), Event{}, false},
		{"not json", "garbage\n", Event{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev, ok := parseEvent([]byte(tc.line))
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, ev)
		})
	}
}

// eventsFixture runs Events on a mock executor with short timings.
type eventsFixture struct {
	pipes *mock.Pipes
	exec  *mock.Executor
	sub   *sse.Subscription[Event]
	ev    *Events
	// starts counts the attempts to start the stream.
	starts atomic.Int32
}

func newEventsFixture(t *testing.T) *eventsFixture {
	t.Helper()
	pipes := &mock.Pipes{}
	exec := &mock.Executor{OnStart: pipes.OnStart}
	bus := &Bus{}
	f := &eventsFixture{pipes: pipes, exec: exec, sub: bus.Subscribe(), ev: &Events{
		Exec: exec, Bus: bus,
		Debounce: 20 * time.Millisecond, BackoffMin: 10 * time.Millisecond, BackoffMax: 40 * time.Millisecond,
		Stable: time.Hour,
	}}
	t.Cleanup(f.sub.Close)
	f.handle(pipes.OnStart)
	return f
}

// handle makes fn answer the attempts to start the stream, and counts
// them.
func (f *eventsFixture) handle(fn func([]string) mock.StreamResult) {
	f.exec.OnStart = func(args []string) mock.StreamResult {
		f.starts.Add(1)
		return fn(args)
	}
}

// take waits for the next batch of events.
func (f *eventsFixture) take(t *testing.T) []Event {
	t.Helper()
	select {
	case <-f.sub.Changed():
		return f.sub.Take()
	case <-time.After(5 * time.Second):
		require.FailNow(t, "no events")
		return nil
	}
}

func TestEventsDebouncesAndPublishes(t *testing.T) {
	f := newEventsFixture(t)
	start(t, f.ev.Run)
	waitFor(t, "stream started", func() bool { return f.pipes.Len() == 1 })

	go func() {
		for range 5 {
			f.pipes.Write(0, line("container", "start", map[string]string{"sind.realm": "sind", "sind.cluster": "dev"}))
		}
		f.pipes.Write(0, line("container", "exec_start: x", map[string]string{"sind.realm": "sind"}))
		f.pipes.Write(0, line("network", "create", map[string]string{"sind.realm": "sind", "name": "sind-lock"}))
	}()
	var got []Event
	for len(got) < 2 {
		got = append(got, f.take(t)...)
	}
	assert.ElementsMatch(t, []Event{
		{Kind: KindChange, Realm: "sind", Cluster: "dev"},
		{Kind: KindLock, Realm: "sind"},
	}, got)
	require.Len(t, f.exec.Calls, 1)
	assert.Equal(t, "docker", f.exec.Calls[0].Name)
	assert.Equal(t, EventsArgs(), f.exec.Calls[0].Args)
}

func TestEventsRestartsWithBackoffAndResyncs(t *testing.T) {
	f := newEventsFixture(t)
	start(t, f.ev.Run)
	waitFor(t, "first stream", func() bool { return f.pipes.Len() == 1 })

	f.pipes.CloseAll() // the stream ends cleanly
	waitFor(t, "second stream", func() bool { return f.pipes.Len() == 2 })
	assert.Contains(t, f.take(t), Event{}, "a restart asks for a resync")

	f.pipes.CloseWithError(1, errors.New("broken pipe"))
	waitFor(t, "third stream", func() bool { return f.pipes.Len() == 3 })
}

func TestEventsRetriesAStartThatFails(t *testing.T) {
	f := newEventsFixture(t)
	f.handle(func(args []string) mock.StreamResult {
		if f.starts.Load() < 3 {
			return mock.StreamResult{Err: errors.New("docker not found")}
		}
		return f.pipes.OnStart(args)
	})
	start(t, f.ev.Run)
	waitFor(t, "stream running after failures", func() bool { return f.pipes.Len() == 1 })
	assert.EqualValues(t, 3, f.starts.Load())
}

func TestEventsBackoffGrowsAndResets(t *testing.T) {
	f := newEventsFixture(t)
	var starts []time.Time
	f.handle(func([]string) mock.StreamResult {
		starts = append(starts, time.Now())
		return mock.StreamResult{Err: errors.New("down")}
	})
	f.ev.BackoffMin, f.ev.BackoffMax = 20*time.Millisecond, 80*time.Millisecond
	stop := start(t, f.ev.Run)
	waitFor(t, "five attempts", func() bool { return f.starts.Load() >= 5 })
	stop()
	// 20, 40, 80, 80 ms between the attempts, not less.
	require.GreaterOrEqual(t, len(starts), 5)
	for i, min := range []time.Duration{20, 40, 80, 80} {
		assert.GreaterOrEqual(t, starts[i+1].Sub(starts[i]), min*time.Millisecond, "wait %d", i)
	}
}

func TestEventsStableStreamResetsBackoff(t *testing.T) {
	f := newEventsFixture(t)
	f.ev.Stable = time.Nanosecond
	f.ev.BackoffMin, f.ev.BackoffMax = 10*time.Millisecond, time.Hour
	start(t, f.ev.Run)
	for i := 1; i <= 4; i++ {
		waitFor(t, "stream", func() bool { return f.pipes.Len() == i })
		f.pipes.CloseAll()
	}
	// Without the reset the fourth wait would be 80 ms and the test would
	// need over a second to get here; the check is that it did.
}

func TestEventsEndsWithContextWhileStreaming(t *testing.T) {
	f := newEventsFixture(t)
	stop := start(t, f.ev.Run)
	waitFor(t, "stream", func() bool { return f.pipes.Len() == 1 })
	stop()
}

func TestEventsEndsWithContextWhileWaiting(t *testing.T) {
	f := newEventsFixture(t)
	f.handle(func([]string) mock.StreamResult { return mock.StreamResult{Err: errors.New("down")} })
	f.ev.BackoffMin, f.ev.BackoffMax = time.Hour, time.Hour
	stop := start(t, f.ev.Run)
	waitFor(t, "first attempt", func() bool { return f.starts.Load() == 1 })
	stop()
}

func TestEventsEndsWhileThePublisherIsBlocked(t *testing.T) {
	// The reader holds an event that nobody takes: the context still ends
	// the stream.
	f := newEventsFixture(t)
	f.ev.Debounce = time.Hour
	stop := start(t, f.ev.Run)
	waitFor(t, "stream", func() bool { return f.pipes.Len() == 1 })
	go func() {
		for range 200 {
			f.pipes.Write(0, line("container", "start", map[string]string{"sind.realm": "sind"}))
		}
	}()
	time.Sleep(50 * time.Millisecond)
	stop()
}

func TestEventsDefaults(t *testing.T) {
	assert.NotNil(t, logOr(nil))
	assert.NotNil(t, logOr(slog.Default()))
}

func TestReadEventsStopsWithContext(t *testing.T) {
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	out := make(chan Event) // nobody reads
	done := make(chan error, 1)
	go func() { done <- readEvents(ctx, pr, out) }()
	_, _ = pw.Write([]byte(line("container", "start", map[string]string{"sind.realm": "sind"})))
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "readEvents did not return")
	}
}
