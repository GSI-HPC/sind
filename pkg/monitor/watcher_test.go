// SPDX-License-Identifier: LGPL-3.0-or-later

package monitor

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWatcher_BroadcastsEvents(t *testing.T) {
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	m := &mock.Executor{OnStart: pipes.OnStart}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	w := NewWatcher(m, "sind-dev-", "dev")
	sub := w.Subscribe()

	nodes := []NodeTarget{{ShortName: "controller", Container: "sind-dev-controller"}}
	err := w.Start(ctx, nodes)
	require.NoError(t, err, "Start")

	// Send a docker event (pipe 0 = docker events).
	pipes.Write(0, `{"Type":"container","Action":"start","Actor":{"Attributes":{"name":"sind-dev-controller"}},"time":1}`+"\n")

	select {
	case ev := <-sub:
		assert.Equal(t, EventContainerStart, ev.Kind)
		assert.Equal(t, "controller", ev.Node)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for event")
	}

	cancel()
	pipes.CloseAll()
	w.Wait()
}

func TestWatcher_SystemdEvents(t *testing.T) {
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	m := &mock.Executor{OnStart: pipes.OnStart}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	w := NewWatcher(m, "sind-dev-", "dev")
	sub := w.Subscribe()

	nodes := []NodeTarget{{ShortName: "controller", Container: "sind-dev-controller"}}
	err := w.Start(ctx, nodes)
	require.NoError(t, err, "Start")

	// Send a systemd event (pipe 1 = systemd monitor for controller).
	pipes.Write(1, `{"type":"signal","endian":"l","flags":1,"version":1,"cookie":100,"timestamp-realtime":1000000,"sender":":1.1","path":"/org/freedesktop/systemd1/unit/munge_2eservice","interface":"org.freedesktop.DBus.Properties","member":"PropertiesChanged","payload":{"type":"sa{sv}as","data":["org.freedesktop.systemd1.Unit",{"ActiveState":{"type":"s","data":"active"}},["Conditions"]]}}`+"\n")

	select {
	case ev := <-sub:
		assert.Equal(t, EventUnitActive, ev.Kind)
		assert.Equal(t, "munge.service", ev.Unit)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for event")
	}

	cancel()
	pipes.CloseAll()
	w.Wait()
}

func TestWatcher_AddNodes(t *testing.T) {
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	m := &mock.Executor{OnStart: pipes.OnStart}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	w := NewWatcher(m, "sind-dev-", "dev")
	sub := w.Subscribe()

	// Start with no nodes, then add one after.
	err := w.Start(ctx, nil)
	require.NoError(t, err, "Start")

	w.AddNodes(ctx, []NodeTarget{{ShortName: "controller", Container: "sind-dev-controller"}})

	// pipe 0 = docker events, pipe 1 = systemd monitor added via AddNodes.
	pipes.Write(1, `{"type":"signal","endian":"l","flags":1,"version":1,"cookie":100,"timestamp-realtime":1000000,"sender":":1.1","path":"/org/freedesktop/systemd1/unit/munge_2eservice","interface":"org.freedesktop.DBus.Properties","member":"PropertiesChanged","payload":{"type":"sa{sv}as","data":["org.freedesktop.systemd1.Unit",{"ActiveState":{"type":"s","data":"active"}},["Conditions"]]}}`+"\n")

	select {
	case ev := <-sub:
		assert.Equal(t, EventUnitActive, ev.Kind)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for event")
	}

	cancel()
	pipes.CloseAll()
	w.Wait()
}

func TestWatcher_MultipleSubscribers(t *testing.T) {
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	m := &mock.Executor{OnStart: pipes.OnStart}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	w := NewWatcher(m, "sind-dev-", "dev")
	sub1 := w.Subscribe()
	sub2 := w.Subscribe()

	nodes := []NodeTarget{{ShortName: "controller", Container: "sind-dev-controller"}}
	err := w.Start(ctx, nodes)
	require.NoError(t, err, "Start")

	pipes.Write(0, `{"Type":"container","Action":"start","Actor":{"Attributes":{"name":"sind-dev-controller"}},"time":1}`+"\n")

	for i, sub := range []<-chan Event{sub1, sub2} {
		select {
		case ev := <-sub:
			assert.Equal(t, EventContainerStart, ev.Kind, "subscriber %d", i)
		case <-time.After(2 * time.Second):
			t.Fatalf("subscriber %d: timeout", i)
		}
	}

	cancel()
	pipes.CloseAll()
	w.Wait()
}

func TestWatcher_Unsubscribe(t *testing.T) {
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	m := &mock.Executor{OnStart: pipes.OnStart}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	w := NewWatcher(m, "sind-dev-", "dev")
	sub1 := w.Subscribe()
	sub2 := w.Subscribe()
	w.Unsubscribe(sub1)

	nodes := []NodeTarget{{ShortName: "controller", Container: "sind-dev-controller"}}
	err := w.Start(ctx, nodes)
	require.NoError(t, err, "Start")

	pipes.Write(0, `{"Type":"container","Action":"start","Actor":{"Attributes":{"name":"sind-dev-controller"}},"time":1}`+"\n")

	select {
	case ev := <-sub2:
		assert.Equal(t, EventContainerStart, ev.Kind)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for event on sub2")
	}

	// sub1 should be closed after Unsubscribe.
	_, ok := <-sub1
	assert.False(t, ok, "sub1 should be closed after Unsubscribe")

	cancel()
	pipes.CloseAll()
	w.Wait()
}

func TestWatcher_UnsubscribeNonexistent(_ *testing.T) {
	m := &mock.Executor{}
	w := NewWatcher(m, "sind-dev-", "dev")
	other := make(chan Event)
	w.Unsubscribe(other)
}

func TestWatcher_FullSubscriberDropsEvents(t *testing.T) {
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	m := &mock.Executor{OnStart: pipes.OnStart}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	w := NewWatcher(m, "sind-dev-", "dev")
	sub := w.Subscribe()

	nodes := []NodeTarget{{ShortName: "controller", Container: "sind-dev-controller"}}
	err := w.Start(ctx, nodes)
	require.NoError(t, err, "Start")

	for i := range 70 {
		pipes.Write(0, `{"Type":"container","Action":"start","Actor":{"Attributes":{"name":"sind-dev-controller"}},"time":`+
			string(rune('0'+i%10))+`}`+"\n")
	}

	time.Sleep(50 * time.Millisecond)

	drained := 0
	for {
		select {
		case <-sub:
			drained++
		default:
			goto done
		}
	}
done:
	assert.LessOrEqual(t, drained, 64, "expected at most 64 (buffer size)")

	cancel()
	pipes.CloseAll()
	w.Wait()
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

	nodes := []NodeTarget{{ShortName: "controller", Container: docker.ContainerName("sind-dev-controller")}}
	err := w.Start(ctx, nodes)
	require.NoError(t, err, "Start")

	select {
	case ev := <-sub:
		assert.Equal(t, EventMonitorError, ev.Kind)
		assert.Equal(t, "controller", ev.Node)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for monitor error event")
	}

	cancel()
	_ = dockerPW.Close()
	w.Wait()
}

func TestWatcher_DockerMonitorRunFailure(t *testing.T) {
	// When docker events stream fails mid-flight, an EventMonitorError
	// should be emitted to subscribers.
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	m := &mock.Executor{OnStart: pipes.OnStart}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	w := NewWatcher(m, "sind-dev-", "dev")
	sub := w.Subscribe()

	err := w.Start(ctx, nil)
	require.NoError(t, err, "Start")

	// Close the docker events pipe writer with an error to simulate
	// a stream failure. The scanner returns the pipe error.
	pipes.CloseWithError(0, errors.New("connection reset"))

	select {
	case ev := <-sub:
		assert.Equal(t, EventMonitorError, ev.Kind)
		assert.Contains(t, ev.Detail, "docker events")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for monitor error event")
	}

	cancel()
	w.Wait()
}

func TestWatcher_SystemdMonitorRunFailure(t *testing.T) {
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	m := &mock.Executor{OnStart: pipes.OnStart}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	w := NewWatcher(m, "sind-dev-", "dev")
	sub := w.Subscribe()

	nodes := []NodeTarget{{ShortName: "controller", Container: "sind-dev-controller"}}
	err := w.Start(ctx, nodes)
	require.NoError(t, err, "Start")

	// Close the systemd monitor pipe (index 1) with an error.
	pipes.CloseWithError(1, errors.New("container stopped"))

	select {
	case ev := <-sub:
		assert.Equal(t, EventMonitorError, ev.Kind)
		assert.Equal(t, "controller", ev.Node)
		assert.Contains(t, ev.Detail, "systemd monitor")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for monitor error event")
	}

	cancel()
	pipes.CloseAll()
	w.Wait()
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
