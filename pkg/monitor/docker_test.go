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

	events := runOn(t, NewDockerMonitor("sind-dev-").Run, input)
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

// TestDockerMonitor_LongLine checks a line beyond bufio's default limit:
// the event of a node whose sind.users label lists thousands of users.
func TestDockerMonitor_LongLine(t *testing.T) {
	users := strings.Repeat("user:2001:2001,", 10000)
	input := `{"Type":"container","Action":"start","Actor":{"Attributes":{"name":"sind-dev-worker-0","sind.users":"` + users + `"}},"time":1234567890}` + "\n"
	require.Greater(t, len(input), 64*1024)
	events := runOn(t, NewDockerMonitor("sind-dev-").Run, input)
	require.Len(t, events, 1)
	assert.Equal(t, "worker-0", events[0].Node)
}

func TestDockerMonitor_DieDetail(t *testing.T) {
	input := `{"Type":"container","Action":"die","Actor":{"Attributes":{"name":"sind-dev-controller","exitCode":"1"}},"time":1234567890}` + "\n"
	events := runOn(t, NewDockerMonitor("sind-dev-").Run, input)
	require.Len(t, events, 1)
	assert.Equal(t, "exitCode=1", events[0].Detail)
}

func TestDockerMonitor_Skips(t *testing.T) {
	for _, tt := range []struct{ name, input string }{
		{"unknown action", `{"Type":"container","Action":"create","Actor":{"Attributes":{"name":"sind-dev-controller"}},"time":1234567890}` + "\n"},
		{"foreign container", startLine("other-container")},
		{"prefix-only name", startLine("sind-dev-")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Empty(t, runOn(t, NewDockerMonitor("sind-dev-").Run, tt.input))
		})
	}
}

func TestDockerMonitor_SkipsMalformedJSON(t *testing.T) {
	events := runOn(t, NewDockerMonitor("sind-dev-").Run, "not json\n"+startLine("sind-dev-controller"))
	require.Len(t, events, 1)
	assert.Equal(t, EventContainerStart, events[0].Kind)
}

func TestDockerEventsArgs(t *testing.T) {
	args := DockerEventsArgs("mycluster")
	want := []string{
		"events",
		"--filter", "type=container",
		"--filter", "label=sind.cluster=mycluster",
		"--filter", "event=die",
		"--filter", "event=oom",
		"--filter", "event=pause",
		"--filter", "event=start",
		"--filter", "event=unpause",
		"--format", "{{json .}}",
	}
	assert.Equal(t, want, args)
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

// runOn runs a monitor's Run on input and returns the events it sent.
func runOn(t *testing.T, run func(context.Context, io.Reader, chan<- Event) error, input string) []Event {
	t.Helper()
	ch := make(chan Event, 10)
	require.NoError(t, run(t.Context(), strings.NewReader(input), ch), "Run")
	close(ch)
	var events []Event
	for ev := range ch {
		events = append(events, ev)
	}
	return events
}
