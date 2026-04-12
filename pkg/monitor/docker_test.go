// SPDX-License-Identifier: LGPL-3.0-or-later

package monitor

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDockerMonitor_Run(t *testing.T) {
	input := strings.Join([]string{
		`{"Type":"container","Action":"start","Actor":{"Attributes":{"name":"sind-dev-controller","sind.cluster":"dev"}},"time":1234567890}`,
		`{"Type":"container","Action":"die","Actor":{"Attributes":{"name":"sind-dev-worker-0","sind.cluster":"dev","exitCode":"137"}},"time":1234567891}`,
		`{"Type":"container","Action":"oom","Actor":{"Attributes":{"name":"sind-dev-worker-1","sind.cluster":"dev"}},"time":1234567892}`,
		`{"Type":"container","Action":"pause","Actor":{"Attributes":{"name":"sind-dev-controller","sind.cluster":"dev"}},"time":1234567893}`,
		`{"Type":"container","Action":"unpause","Actor":{"Attributes":{"name":"sind-dev-controller","sind.cluster":"dev"}},"time":1234567894}`,
	}, "\n")

	m := NewDockerMonitor("sind-dev-")
	ch := make(chan Event, 10)
	err := m.Run(t.Context(), strings.NewReader(input), ch)
	require.NoError(t, err, "Run")
	close(ch)

	events := drainEvents(ch)
	require.Len(t, events, 5)

	tests := []struct {
		kind EventKind
		node string
	}{
		{EventContainerStart, "controller"},
		{EventContainerDie, "worker-0"},
		{EventContainerOOM, "worker-1"},
		{EventContainerPause, "controller"},
		{EventContainerUnpause, "controller"},
	}

	for i, tt := range tests {
		assert.Equal(t, tt.kind, events[i].Kind, "event[%d].Kind", i)
		assert.Equal(t, tt.node, events[i].Node, "event[%d].Node", i)
	}
}

func TestDockerMonitor_DieDetail(t *testing.T) {
	input := `{"Type":"container","Action":"die","Actor":{"Attributes":{"name":"sind-dev-controller","exitCode":"1"}},"time":1234567890}` + "\n"
	m := NewDockerMonitor("sind-dev-")
	ch := make(chan Event, 10)

	err := m.Run(t.Context(), strings.NewReader(input), ch)
	require.NoError(t, err, "Run")
	close(ch)

	events := drainEvents(ch)
	require.Len(t, events, 1)
	assert.Equal(t, "exitCode=1", events[0].Detail)
}

func TestDockerMonitor_SkipsUnknownActions(t *testing.T) {
	input := `{"Type":"container","Action":"create","Actor":{"Attributes":{"name":"sind-dev-controller"}},"time":1234567890}` + "\n"
	m := NewDockerMonitor("sind-dev-")
	ch := make(chan Event, 10)

	err := m.Run(t.Context(), strings.NewReader(input), ch)
	require.NoError(t, err, "Run")
	close(ch)

	events := drainEvents(ch)
	assert.Empty(t, events)
}

func TestDockerMonitor_SkipsForeignContainers(t *testing.T) {
	input := `{"Type":"container","Action":"start","Actor":{"Attributes":{"name":"other-container"}},"time":1234567890}` + "\n"
	m := NewDockerMonitor("sind-dev-")
	ch := make(chan Event, 10)

	err := m.Run(t.Context(), strings.NewReader(input), ch)
	require.NoError(t, err, "Run")
	close(ch)

	events := drainEvents(ch)
	assert.Empty(t, events)
}

func TestDockerMonitor_SkipsMalformedJSON(t *testing.T) {
	input := "not json\n" +
		`{"Type":"container","Action":"start","Actor":{"Attributes":{"name":"sind-dev-controller"}},"time":1234567890}` + "\n"
	m := NewDockerMonitor("sind-dev-")
	ch := make(chan Event, 10)

	err := m.Run(t.Context(), strings.NewReader(input), ch)
	require.NoError(t, err, "Run")
	close(ch)

	events := drainEvents(ch)
	require.Len(t, events, 1)
	assert.Equal(t, EventContainerStart, events[0].Kind)
}

func TestDockerEventsArgs(t *testing.T) {
	args := DockerEventsArgs("mycluster")
	want := []string{
		"events",
		"--filter", "type=container",
		"--filter", "label=sind.cluster=mycluster",
		"--format", "{{json .}}",
	}
	assert.Equal(t, want, args)
}

func TestDockerMonitor_SkipsPrefixOnlyName(t *testing.T) {
	input := `{"Type":"container","Action":"start","Actor":{"Attributes":{"name":"sind-dev-"}},"time":1234567890}` + "\n"
	m := NewDockerMonitor("sind-dev-")
	ch := make(chan Event, 10)

	err := m.Run(t.Context(), strings.NewReader(input), ch)
	require.NoError(t, err, "Run")
	close(ch)

	events := drainEvents(ch)
	assert.Empty(t, events, "prefix-only name should be skipped")
}

func TestDockerMonitor_ContextCancelledMidScan(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())

	pr, pw := io.Pipe()
	m := NewDockerMonitor("sind-dev-")
	ch := make(chan Event, 1)

	done := make(chan error, 1)
	go func() {
		done <- m.Run(ctx, pr, ch)
	}()

	// Write a valid event so the scanner reads one line.
	_, _ = pw.Write([]byte(`{"Type":"container","Action":"start","Actor":{"Attributes":{"name":"sind-dev-controller"}},"time":1}` + "\n"))

	// Drain the event so we know the loop iterated.
	<-ch

	// Cancel and close the pipe. The scanner.Scan() will return false
	// on the next iteration because the pipe is closed.
	cancel()
	_ = pw.Close()

	err := <-done
	assert.NoError(t, err, "scanner sees EOF")
}

func TestDockerMonitor_ContextCancelledDuringSend(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())

	pr, pw := io.Pipe()
	m := NewDockerMonitor("sind-dev-")
	// Unbuffered channel — send will block.
	ch := make(chan Event)

	done := make(chan error, 1)
	go func() {
		done <- m.Run(ctx, pr, ch)
	}()

	// Write an event. The send to ch will block because nobody reads.
	_, _ = pw.Write([]byte(`{"Type":"container","Action":"start","Actor":{"Attributes":{"name":"sind-dev-controller"}},"time":1}` + "\n"))

	// Cancel the context to unblock the select.
	cancel()
	_ = pw.Close()

	err := <-done
	// Either context.Canceled (cancel won the race) or nil (pipe EOF
	// reached the scanner first) are valid outcomes.
	if err != nil {
		assert.ErrorIs(t, err, context.Canceled)
	}
}

func drainEvents(ch <-chan Event) []Event {
	var events []Event
	for ev := range ch {
		events = append(events, ev)
	}
	return events
}
