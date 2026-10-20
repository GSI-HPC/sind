// SPDX-License-Identifier: LGPL-3.0-or-later

package monitor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// signal is a busctl monitor line of a PropertiesChanged signal of
// munge.service with the payload data data.
func signal(data string) string {
	return `{"type":"signal","endian":"l","flags":1,"version":1,"cookie":100,"timestamp-realtime":1000000,"sender":":1.1","path":"/org/freedesktop/systemd1/unit/munge_2eservice","interface":"org.freedesktop.DBus.Properties","member":"PropertiesChanged","payload":{"type":"sa{sv}as","data":` + data + `}}` + "\n"
}

func TestSystemdMonitor_UnitActive(t *testing.T) {
	// Real busctl output for munge.service reaching ActiveState=active.
	input := `{"type":"signal","endian":"l","flags":1,"version":1,"cookie":516,"timestamp-realtime":1775658823092599,"sender":":1.1","path":"/org/freedesktop/systemd1/unit/munge_2eservice","interface":"org.freedesktop.DBus.Properties","member":"PropertiesChanged","payload":{"type":"sa{sv}as","data":["org.freedesktop.systemd1.Unit",{"ActiveState":{"type":"s","data":"active"},"SubState":{"type":"s","data":"running"}},["Conditions","Asserts"]]}}` + "\n"

	events := runOn(t, NewSystemdMonitor("controller", "sind-dev-controller").Run, input)
	require.Len(t, events, 1)

	ev := events[0]
	assert.Equal(t, EventUnitActive, ev.Kind)
	assert.Equal(t, "controller", ev.Node)
	assert.Equal(t, "munge.service", ev.Unit)
	assert.Equal(t, "ActiveState=active", ev.Detail)
}

func TestSystemdMonitor_UnitFailed(t *testing.T) {
	input := `{"type":"signal","endian":"l","flags":1,"version":1,"cookie":100,"timestamp-realtime":1000000,"sender":":1.1","path":"/org/freedesktop/systemd1/unit/slurmd_2eservice","interface":"org.freedesktop.DBus.Properties","member":"PropertiesChanged","payload":{"type":"sa{sv}as","data":["org.freedesktop.systemd1.Unit",{"ActiveState":{"type":"s","data":"failed"}},["Conditions"]]}}` + "\n"

	events := runOn(t, NewSystemdMonitor("worker-0", "sind-dev-worker-0").Run, input)
	require.Len(t, events, 1)
	assert.Equal(t, EventUnitFailed, events[0].Kind)
	assert.Equal(t, "slurmd.service", events[0].Unit)
}

func TestSystemdMonitor_NonStringPropertiesIgnored(t *testing.T) {
	// Unit interface with mixed property types — non-string properties should be
	// silently ignored while ActiveState (string) is still extracted.
	input := signal(`["org.freedesktop.systemd1.Unit",{"ActiveState":{"type":"s","data":"active"},"ConditionResult":{"type":"b","data":true},"Job":{"type":"(uo)","data":[0,"/"]}},["Conditions"]]`)

	events := runOn(t, NewSystemdMonitor("controller", "sind-dev-controller").Run, input)
	require.Len(t, events, 1)
	assert.Equal(t, EventUnitActive, events[0].Kind)
}

func TestSystemdMonitor_Skips(t *testing.T) {
	for _, tt := range []struct{ name, input string }{
		{"not a signal", `{"type":"method_call","path":"/foo","member":"Bar","payload":{"data":[]}}` + "\n"},
		{"not the Unit interface", signal(`["org.freedesktop.systemd1.Service",{"MainPID":{"type":"u","data":298}},["ExecStart"]]`)},
		{"transient state", signal(`["org.freedesktop.systemd1.Unit",{"ActiveState":{"type":"s","data":"activating"}},["Conditions"]]`)},
		{"no ActiveState", signal(`["org.freedesktop.systemd1.Unit",{"SubState":{"type":"s","data":"running"}},["Conditions"]]`)},
		{"payload data not an array", signal(`"notanarray"`)},
		{"interface name not a string", signal(`[42,{},["Conditions"]]`)},
		{"properties not a map", signal(`["org.freedesktop.systemd1.Unit","notamap",["Conditions"]]`)},
		{"string property not a string", signal(`["org.freedesktop.systemd1.Unit",{"ActiveState":{"type":"s","data":42}},["Conditions"]]`)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Empty(t, runOn(t, NewSystemdMonitor("controller", "sind-dev-controller").Run, tt.input))
		})
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
