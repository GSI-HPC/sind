// SPDX-License-Identifier: LGPL-3.0-or-later

package monitor

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemdMonitor_UnitActive(t *testing.T) {
	// Real busctl output for munge.service reaching ActiveState=active.
	input := `{"type":"signal","endian":"l","flags":1,"version":1,"cookie":516,"timestamp-realtime":1775658823092599,"sender":":1.1","path":"/org/freedesktop/systemd1/unit/munge_2eservice","interface":"org.freedesktop.DBus.Properties","member":"PropertiesChanged","payload":{"type":"sa{sv}as","data":["org.freedesktop.systemd1.Unit",{"ActiveState":{"type":"s","data":"active"},"SubState":{"type":"s","data":"running"}},["Conditions","Asserts"]]}}` + "\n"

	m := NewSystemdMonitor("controller", "sind-dev-controller")
	ch := make(chan Event, 10)
	err := m.Run(t.Context(), strings.NewReader(input), ch)
	require.NoError(t, err, "Run")
	close(ch)

	events := drainEvents(ch)
	require.Len(t, events, 1)

	ev := events[0]
	assert.Equal(t, EventUnitActive, ev.Kind)
	assert.Equal(t, "controller", ev.Node)
	assert.Equal(t, "munge.service", ev.Unit)
	assert.Equal(t, "ActiveState=active", ev.Detail)
}

func TestSystemdMonitor_UnitFailed(t *testing.T) {
	input := `{"type":"signal","endian":"l","flags":1,"version":1,"cookie":100,"timestamp-realtime":1000000,"sender":":1.1","path":"/org/freedesktop/systemd1/unit/slurmd_2eservice","interface":"org.freedesktop.DBus.Properties","member":"PropertiesChanged","payload":{"type":"sa{sv}as","data":["org.freedesktop.systemd1.Unit",{"ActiveState":{"type":"s","data":"failed"}},["Conditions"]]}}` + "\n"

	m := NewSystemdMonitor("worker-0", "sind-dev-worker-0")
	ch := make(chan Event, 10)
	err := m.Run(t.Context(), strings.NewReader(input), ch)
	require.NoError(t, err, "Run")
	close(ch)

	events := drainEvents(ch)
	require.Len(t, events, 1)
	assert.Equal(t, EventUnitFailed, events[0].Kind)
	assert.Equal(t, "slurmd.service", events[0].Unit)
}

func TestSystemdMonitor_SkipsNonUnitInterface(t *testing.T) {
	// PropertiesChanged on org.freedesktop.systemd1.Service (not .Unit).
	input := `{"type":"signal","endian":"l","flags":1,"version":1,"cookie":100,"timestamp-realtime":1000000,"sender":":1.1","path":"/org/freedesktop/systemd1/unit/munge_2eservice","interface":"org.freedesktop.DBus.Properties","member":"PropertiesChanged","payload":{"type":"sa{sv}as","data":["org.freedesktop.systemd1.Service",{"MainPID":{"type":"u","data":298}},["ExecStart"]]}}` + "\n"

	m := NewSystemdMonitor("controller", "sind-dev-controller")
	ch := make(chan Event, 10)
	err := m.Run(t.Context(), strings.NewReader(input), ch)
	require.NoError(t, err, "Run")
	close(ch)

	events := drainEvents(ch)
	assert.Empty(t, events)
}

func TestSystemdMonitor_SkipsTransientStates(t *testing.T) {
	// ActiveState=activating is transient, should be skipped.
	input := `{"type":"signal","endian":"l","flags":1,"version":1,"cookie":100,"timestamp-realtime":1000000,"sender":":1.1","path":"/org/freedesktop/systemd1/unit/munge_2eservice","interface":"org.freedesktop.DBus.Properties","member":"PropertiesChanged","payload":{"type":"sa{sv}as","data":["org.freedesktop.systemd1.Unit",{"ActiveState":{"type":"s","data":"activating"}},["Conditions"]]}}` + "\n"

	m := NewSystemdMonitor("controller", "sind-dev-controller")
	ch := make(chan Event, 10)
	err := m.Run(t.Context(), strings.NewReader(input), ch)
	require.NoError(t, err, "Run")
	close(ch)

	events := drainEvents(ch)
	assert.Empty(t, events, "transient state should be skipped")
}

func TestSystemdMonitor_SkipsNonSignal(t *testing.T) {
	input := `{"type":"method_call","path":"/foo","member":"Bar","payload":{"data":[]}}` + "\n"

	m := NewSystemdMonitor("controller", "sind-dev-controller")
	ch := make(chan Event, 10)
	err := m.Run(t.Context(), strings.NewReader(input), ch)
	require.NoError(t, err, "Run")
	close(ch)

	events := drainEvents(ch)
	assert.Empty(t, events)
}

func TestSystemdMonitor_SkipsMalformedJSON(t *testing.T) {
	input := "not json\n" +
		`{"type":"signal","endian":"l","flags":1,"version":1,"cookie":100,"timestamp-realtime":1000000,"sender":":1.1","path":"/org/freedesktop/systemd1/unit/munge_2eservice","interface":"org.freedesktop.DBus.Properties","member":"PropertiesChanged","payload":{"type":"sa{sv}as","data":["org.freedesktop.systemd1.Unit",{"ActiveState":{"type":"s","data":"active"}},["Conditions"]]}}` + "\n"

	m := NewSystemdMonitor("controller", "sind-dev-controller")
	ch := make(chan Event, 10)
	err := m.Run(t.Context(), strings.NewReader(input), ch)
	require.NoError(t, err, "Run")
	close(ch)

	events := drainEvents(ch)
	require.Len(t, events, 1, "malformed line skipped, valid line parsed")
}

func TestSystemdMonitor_SkipsNoActiveState(t *testing.T) {
	// PropertiesChanged on .Unit but without ActiveState property.
	input := `{"type":"signal","endian":"l","flags":1,"version":1,"cookie":100,"timestamp-realtime":1000000,"sender":":1.1","path":"/org/freedesktop/systemd1/unit/munge_2eservice","interface":"org.freedesktop.DBus.Properties","member":"PropertiesChanged","payload":{"type":"sa{sv}as","data":["org.freedesktop.systemd1.Unit",{"SubState":{"type":"s","data":"running"}},["Conditions"]]}}` + "\n"

	m := NewSystemdMonitor("controller", "sind-dev-controller")
	ch := make(chan Event, 10)
	err := m.Run(t.Context(), strings.NewReader(input), ch)
	require.NoError(t, err, "Run")
	close(ch)

	events := drainEvents(ch)
	assert.Empty(t, events)
}

func TestSystemdMonitor_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())

	pr, pw := io.Pipe()
	m := NewSystemdMonitor("controller", "sind-dev-controller")
	// Unbuffered channel — send blocks.
	ch := make(chan Event)

	done := make(chan error, 1)
	go func() {
		done <- m.Run(ctx, pr, ch)
	}()

	// Write an event. The send will block.
	_, _ = pw.Write([]byte(`{"type":"signal","endian":"l","flags":1,"version":1,"cookie":100,"timestamp-realtime":1000000,"sender":":1.1","path":"/org/freedesktop/systemd1/unit/munge_2eservice","interface":"org.freedesktop.DBus.Properties","member":"PropertiesChanged","payload":{"type":"sa{sv}as","data":["org.freedesktop.systemd1.Unit",{"ActiveState":{"type":"s","data":"active"}},["Conditions"]]}}` + "\n"))

	cancel()
	_ = pw.Close()

	err := <-done
	// Either context.Canceled (cancel won the race) or nil (pipe EOF
	// reached the scanner first) are valid outcomes.
	if err != nil {
		assert.ErrorIs(t, err, context.Canceled)
	}
}

func TestUnitNameFromPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/org/freedesktop/systemd1/unit/munge_2eservice", "munge.service"},
		{"/org/freedesktop/systemd1/unit/slurmd_2eservice", "slurmd.service"},
		{"/org/freedesktop/systemd1/unit/slurmctld_2eservice", "slurmctld.service"},
		{"/org/freedesktop/systemd1/unit/systemd_2dtmpfiles_2dclean_2eservice", "systemd-tmpfiles-clean.service"},
		{"/org/freedesktop/systemd1/unit/sshd_2eservice", "sshd.service"},
		{"/org/freedesktop/systemd1", ""},
		{"/other/path", ""},
	}

	for _, tt := range tests {
		got := unitNameFromPath(tt.path)
		assert.Equal(t, tt.want, got, "unitNameFromPath(%q)", tt.path)
	}
}

func TestDbusPathUnescape(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"munge_2eservice", "munge.service"},
		{"systemd_2dtmpfiles_2dclean_2eservice", "systemd-tmpfiles-clean.service"},
		{"plain", "plain"},
		{"_2F", "/"},         // uppercase hex
		{"_2f", "/"},         // lowercase hex
		{"trail_", "trail_"}, // incomplete escape at end
		{"_zz", "_zz"},       // invalid hex digits
	}

	for _, tt := range tests {
		got := dbusPathUnescape(tt.input)
		assert.Equal(t, tt.want, got, "dbusPathUnescape(%q)", tt.input)
	}
}

func TestSystemdMonitorArgs(t *testing.T) {
	args := SystemdMonitorArgs("sind-dev-controller")
	assert.Equal(t, "exec", args[0])
	assert.Equal(t, "sind-dev-controller", args[1])
	assert.Equal(t, "busctl", args[2])
}

func TestSystemdMonitor_SkipsMalformedPayloadData(t *testing.T) {
	// payload.data is not a valid array.
	input := `{"type":"signal","endian":"l","flags":1,"version":1,"cookie":100,"timestamp-realtime":1000000,"sender":":1.1","path":"/org/freedesktop/systemd1/unit/munge_2eservice","interface":"org.freedesktop.DBus.Properties","member":"PropertiesChanged","payload":{"type":"sa{sv}as","data":"notanarray"}}` + "\n"

	m := NewSystemdMonitor("controller", docker.ContainerName("sind-dev-controller"))
	ch := make(chan Event, 10)
	err := m.Run(t.Context(), strings.NewReader(input), ch)
	require.NoError(t, err, "Run")
	close(ch)

	events := drainEvents(ch)
	assert.Empty(t, events)
}

func TestSystemdMonitor_SkipsMalformedInterfaceName(t *testing.T) {
	// First element of data array is not a string.
	input := `{"type":"signal","endian":"l","flags":1,"version":1,"cookie":100,"timestamp-realtime":1000000,"sender":":1.1","path":"/org/freedesktop/systemd1/unit/munge_2eservice","interface":"org.freedesktop.DBus.Properties","member":"PropertiesChanged","payload":{"type":"sa{sv}as","data":[42,{},["Conditions"]]}}` + "\n"

	m := NewSystemdMonitor("controller", docker.ContainerName("sind-dev-controller"))
	ch := make(chan Event, 10)
	err := m.Run(t.Context(), strings.NewReader(input), ch)
	require.NoError(t, err, "Run")
	close(ch)

	events := drainEvents(ch)
	assert.Empty(t, events)
}

func TestSystemdMonitor_NonStringPropertiesIgnored(t *testing.T) {
	// Unit interface with mixed property types — non-string properties should be
	// silently ignored while ActiveState (string) is still extracted.
	input := `{"type":"signal","endian":"l","flags":1,"version":1,"cookie":100,"timestamp-realtime":1000000,"sender":":1.1","path":"/org/freedesktop/systemd1/unit/munge_2eservice","interface":"org.freedesktop.DBus.Properties","member":"PropertiesChanged","payload":{"type":"sa{sv}as","data":["org.freedesktop.systemd1.Unit",{"ActiveState":{"type":"s","data":"active"},"ConditionResult":{"type":"b","data":true},"Job":{"type":"(uo)","data":[0,"/"]}},["Conditions"]]}}` + "\n"

	m := NewSystemdMonitor("controller", docker.ContainerName("sind-dev-controller"))
	ch := make(chan Event, 10)
	err := m.Run(t.Context(), strings.NewReader(input), ch)
	require.NoError(t, err, "Run")
	close(ch)

	events := drainEvents(ch)
	require.Len(t, events, 1)
	assert.Equal(t, EventUnitActive, events[0].Kind)
}

func TestSystemdMonitor_SkipsMalformedStringData(t *testing.T) {
	// Property claims type "s" but data is not a valid JSON string.
	input := `{"type":"signal","endian":"l","flags":1,"version":1,"cookie":100,"timestamp-realtime":1000000,"sender":":1.1","path":"/org/freedesktop/systemd1/unit/munge_2eservice","interface":"org.freedesktop.DBus.Properties","member":"PropertiesChanged","payload":{"type":"sa{sv}as","data":["org.freedesktop.systemd1.Unit",{"ActiveState":{"type":"s","data":42}},["Conditions"]]}}` + "\n"

	m := NewSystemdMonitor("controller", docker.ContainerName("sind-dev-controller"))
	ch := make(chan Event, 10)
	err := m.Run(t.Context(), strings.NewReader(input), ch)
	require.NoError(t, err, "Run")
	close(ch)

	events := drainEvents(ch)
	assert.Empty(t, events)
}

func TestSystemdMonitor_SkipsMalformedProperties(t *testing.T) {
	// Properties element is not a valid map.
	input := `{"type":"signal","endian":"l","flags":1,"version":1,"cookie":100,"timestamp-realtime":1000000,"sender":":1.1","path":"/org/freedesktop/systemd1/unit/munge_2eservice","interface":"org.freedesktop.DBus.Properties","member":"PropertiesChanged","payload":{"type":"sa{sv}as","data":["org.freedesktop.systemd1.Unit","notamap",["Conditions"]]}}` + "\n"

	m := NewSystemdMonitor("controller", docker.ContainerName("sind-dev-controller"))
	ch := make(chan Event, 10)
	err := m.Run(t.Context(), strings.NewReader(input), ch)
	require.NoError(t, err, "Run")
	close(ch)

	events := drainEvents(ch)
	assert.Empty(t, events)
}
