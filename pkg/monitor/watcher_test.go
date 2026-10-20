// SPDX-License-Identifier: LGPL-3.0-or-later

package monitor

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// controller is the node the watcher tests monitor.
var controller = NodeTarget{ShortName: "controller", Container: "sind-dev-controller"}

// startLine is a docker events line of a container's start.
func startLine(name string) string {
	return `{"Type":"container","Action":"start","Actor":{"Attributes":{"name":"` + name + `"}},"time":1}` + "\n"
}

// mungeActive is a busctl monitor line of munge.service reaching active.
const mungeActive = `{"type":"signal","endian":"l","flags":1,"version":1,"cookie":100,"timestamp-realtime":1000000,"sender":":1.1","path":"/org/freedesktop/systemd1/unit/munge_2eservice","interface":"org.freedesktop.DBus.Properties","member":"PropertiesChanged","payload":{"type":"sa{sv}as","data":["org.freedesktop.systemd1.Unit",{"ActiveState":{"type":"s","data":"active"}},["Conditions"]]}}` + "\n"

// startWatcher starts a watcher of cluster dev on fake streams, pipe 0
// the docker events and pipe i+1 the systemd monitor of nodes[i], and
// stops it when the test ends.
func startWatcher(t *testing.T, nodes ...NodeTarget) (*Watcher, *mock.Pipes) {
	t.Helper()
	pipes := &mock.Pipes{}
	ctx, cancel := context.WithCancel(t.Context())
	w := NewWatcher(&mock.Executor{OnStart: pipes.OnStart}, "sind-dev-", "dev")
	require.NoError(t, w.Start(ctx, nodes), "Start")
	t.Cleanup(func() {
		cancel()
		pipes.CloseAll()
		w.Wait()
	})
	return w, pipes
}

// next returns the next event of sub, failing the test after 2 s.
func next(t *testing.T, sub <-chan Event) Event {
	t.Helper()
	select {
	case ev := <-sub:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for an event")
		return Event{}
	}
}

// assertNone asserts that sub has no event waiting.
func assertNone(t *testing.T, sub <-chan Event) {
	t.Helper()
	select {
	case ev := <-sub:
		t.Fatalf("unexpected event %+v", ev)
	default:
	}
}

func TestWatcher_BroadcastsEvents(t *testing.T) {
	w, pipes := startWatcher(t, controller)
	sub := w.Subscribe()
	pipes.Write(0, startLine("sind-dev-controller"))
	ev := next(t, sub)
	assert.Equal(t, EventContainerStart, ev.Kind)
	assert.Equal(t, "controller", ev.Node)
}

func TestWatcher_SystemdEvents(t *testing.T) {
	w, pipes := startWatcher(t, controller)
	sub := w.Subscribe()
	pipes.Write(1, mungeActive)
	ev := next(t, sub)
	assert.Equal(t, EventUnitActive, ev.Kind)
	assert.Equal(t, "munge.service", ev.Unit)
}

func TestWatcher_AddNodes(t *testing.T) {
	w, pipes := startWatcher(t)
	sub := w.Subscribe()
	w.AddNodes([]NodeTarget{controller})
	pipes.Write(1, mungeActive)
	assert.Equal(t, EventUnitActive, next(t, sub).Kind)
}

func TestWatcher_AddNodesBeforeStart(t *testing.T) {
	m := &mock.Executor{}
	w := NewWatcher(m, "sind-dev-", "dev")
	w.AddNodes([]NodeTarget{controller})
	assert.Empty(t, m.Calls)
}

func TestWatcher_AddNodesStopsWithWatcher(t *testing.T) {
	// The monitors AddNodes starts run on the watcher's context: cancelling
	// it ends them, without their streams being closed, and Wait returns.
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	m := &mock.Executor{OnStart: pipes.OnStart}
	ctx, cancel := context.WithCancel(t.Context())
	w := NewWatcher(m, "sind-dev-", "dev")
	require.NoError(t, w.Start(ctx, nil))
	w.AddNodes([]NodeTarget{controller})
	require.Equal(t, 2, pipes.Len())

	cancel()
	done := make(chan struct{})
	go func() {
		w.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after the watcher's context ended")
	}
}

func TestWatcher_MultipleSubscribers(t *testing.T) {
	w, pipes := startWatcher(t, controller)
	sub1, sub2 := w.Subscribe(), w.Subscribe()
	pipes.Write(0, startLine("sind-dev-controller"))
	assert.Equal(t, EventContainerStart, next(t, sub1).Kind)
	assert.Equal(t, EventContainerStart, next(t, sub2).Kind)
}

func TestWatcher_SubscribeTo(t *testing.T) {
	w, pipes := startWatcher(t)
	worker := w.SubscribeTo("sind-dev-worker-0")
	all := w.Subscribe()
	pipes.Write(0, startLine("sind-dev-controller"))
	pipes.Write(0, startLine("sind-dev-worker-0"))
	assert.Equal(t, "controller", next(t, all).Node)
	assert.Equal(t, "worker-0", next(t, all).Node)
	// The worker's subscription got the worker's event only.
	assert.Equal(t, "worker-0", next(t, worker).Node)
	assertNone(t, worker)
}

func TestWatcher_Unsubscribe(t *testing.T) {
	w, pipes := startWatcher(t, controller)
	sub1, sub2 := w.Subscribe(), w.Subscribe()
	w.Unsubscribe(sub1)
	pipes.Write(0, startLine("sind-dev-controller"))
	assert.Equal(t, EventContainerStart, next(t, sub2).Kind)
	_, ok := <-sub1
	assert.False(t, ok, "sub1 should be closed after Unsubscribe")
}

func TestWatcher_UnsubscribeNonexistent(_ *testing.T) {
	m := &mock.Executor{}
	w := NewWatcher(m, "sind-dev-", "dev")
	other := make(chan Event)
	w.Unsubscribe(other)
}

func TestWatcher_FullSubscriberDropsEvents(t *testing.T) {
	w, pipes := startWatcher(t, controller)
	sub := w.Subscribe()
	for range 70 {
		pipes.Write(0, startLine("sind-dev-controller"))
	}
	time.Sleep(50 * time.Millisecond)
	assert.LessOrEqual(t, len(sub), 64, "expected at most 64 (buffer size)")
}

func TestWatcher_SystemdMonitorStartFailure(t *testing.T) {
	var dockerPW *io.PipeWriter
	callCount := 0
	m := &mock.Executor{
		OnStart: func(_ []string) mock.StreamResult {
			callCount++
			if callCount == 1 {
				pr, pw := io.Pipe()
				dockerPW = pw
				return mock.StreamResult{Reader: pr}
			}
			return mock.StreamResult{Err: errors.New("busctl not available")}
		},
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	w := NewWatcher(m, "sind-dev-", "dev")
	sub := w.Subscribe()

	require.NoError(t, w.Start(ctx, []NodeTarget{controller}), "Start")

	ev := next(t, sub)
	assert.Equal(t, EventMonitorError, ev.Kind)
	assert.Equal(t, "controller", ev.Node)

	cancel()
	_ = dockerPW.Close()
	w.Wait()
}

func TestWatcher_MonitorStreamFails(t *testing.T) {
	// A stream that fails or ends while the watcher runs, as busctl's does
	// when its node stops or the image has no busctl, is a monitor error:
	// the monitor's events are missing from then on.
	for _, tt := range []struct {
		name   string
		pipe   int
		err    error
		node   string
		detail string
	}{
		{"docker events failed", 0, errors.New("connection reset"), "", "docker events stream failed"},
		{"systemd monitor failed", 1, errors.New("container stopped"), "controller", "systemd monitor stream failed"},
		{"docker events ended", 0, nil, "", "docker events stream ended"},
		{"systemd monitor ended", 1, nil, "controller", "systemd monitor stream ended"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w, pipes := startWatcher(t, controller)
			sub := w.Subscribe()
			pipes.CloseWithError(tt.pipe, tt.err)
			ev := next(t, sub)
			assert.Equal(t, EventMonitorError, ev.Kind)
			assert.Equal(t, tt.node, ev.Node)
			assert.Equal(t, tt.detail, ev.Detail)
			if tt.err == nil {
				assert.ErrorIs(t, ev.Err, errStreamEnded)
			}
		})
	}
}

func TestWatcher_SubscribeToGetsWatcherErrors(t *testing.T) {
	// A subscription to one container gets the monitor errors of the
	// whole watcher, not those of another container's monitor.
	w, _ := startWatcher(t)
	worker := w.SubscribeTo("sind-dev-worker-0")
	w.emit(t.Context(), Event{Kind: EventMonitorError, Node: "controller", Container: "sind-dev-controller"})
	w.emit(t.Context(), Event{Kind: EventMonitorError, Detail: "docker events stream failed"})
	ev := next(t, worker)
	assert.Empty(t, ev.Container)
	assert.Equal(t, "docker events stream failed", ev.Detail)
	assertNone(t, worker)
}

func TestWatcher_DockerMonitorStartFailure(t *testing.T) {
	m := &mock.Executor{
		OnStart: func(_ []string) mock.StreamResult {
			return mock.StreamResult{Err: errors.New("docker not available")}
		},
	}

	w := NewWatcher(m, "sind-dev-", "dev")
	err := w.Start(t.Context(), nil)
	require.Error(t, err)
}

func TestWatcher_WaitClosesSubscribers(t *testing.T) {
	pipes := &mock.Pipes{}

	m := &mock.Executor{OnStart: pipes.OnStart}
	ctx, cancel := context.WithCancel(t.Context())

	w := NewWatcher(m, "sind-dev-", "dev")
	sub := w.Subscribe()

	err := w.Start(ctx, nil)
	require.NoError(t, err, "Start")

	cancel()
	pipes.CloseAll()
	w.Wait()

	_, ok := <-sub
	assert.False(t, ok, "subscriber channel should be closed after Wait")
}

// TestWatcher_Emit_SendsWhenChannelHasRoom covers the normal path: emit
// delivers the event via internalCh when the buffer is not full.
func TestWatcher_Emit_SendsWhenChannelHasRoom(t *testing.T) {
	w := NewWatcher(&mock.Executor{}, "sind-dev-", "dev")

	w.emit(t.Context(), Event{Kind: EventMonitorError, Detail: "boom"})

	select {
	case ev := <-w.internalCh:
		assert.Equal(t, EventMonitorError, ev.Kind)
		assert.Equal(t, "boom", ev.Detail)
	default:
		t.Fatal("expected event on internalCh")
	}
}

// TestWatcher_Emit_CtxDoneWhenChannelFull covers the teardown path: when
// the internal channel is full and the context is cancelled, emit returns
// via the ctx.Done() branch rather than blocking forever.
func TestWatcher_Emit_CtxDoneWhenChannelFull(t *testing.T) {
	w := NewWatcher(&mock.Executor{}, "sind-dev-", "dev")

	for range cap(w.internalCh) {
		w.internalCh <- Event{}
	}
	require.Equal(t, cap(w.internalCh), len(w.internalCh), "precondition: channel full")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	done := make(chan struct{})
	go func() {
		w.emit(ctx, Event{Kind: EventMonitorError, Detail: "drop"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("emit did not return on cancelled ctx with full channel")
	}

	assert.Equal(t, cap(w.internalCh), len(w.internalCh), "no slot should have been consumed")
}
