// SPDX-License-Identifier: LGPL-3.0-or-later

package monitor

import (
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/pkg/docker"
)

// busctlSignal is the JSON structure emitted by busctl monitor --json=short
// for PropertiesChanged signals on org.freedesktop.systemd1.
type busctlSignal struct {
	Type    string `json:"type"`
	Path    string `json:"path"`
	Member  string `json:"member"`
	Payload struct {
		Data json.RawMessage `json:"data"`
	} `json:"payload"`
	TimestampRealtime int64 `json:"timestamp-realtime"`
}

// SystemdMonitor reads busctl monitor output from inside a container
// and emits Events for systemd unit state changes.
type SystemdMonitor struct {
	node      string
	container docker.ContainerName
}

// NewSystemdMonitor creates a monitor for a single node container.
func NewSystemdMonitor(node string, container docker.ContainerName) *SystemdMonitor {
	return &SystemdMonitor{node: node, container: container}
}

// Run reads busctl monitor --json=short from r and sends the unit state
// changes it reports to ch (see scanEvents).
func (m *SystemdMonitor) Run(ctx context.Context, r io.Reader, ch chan<- Event) error {
	// busctl JSON lines can be very long.
	return scanEvents(ctx, r, ch, "systemd monitor", 512*1024, func(sig busctlSignal) (Event, bool) {
		if sig.Type != "signal" || sig.Member != "PropertiesChanged" {
			return Event{}, false
		}
		return m.parsePropertiesChanged(sig)
	})
}

// parsePropertiesChanged extracts an Event from a PropertiesChanged signal.
// Returns false if the signal is not relevant (wrong interface, no state change).
func (m *SystemdMonitor) parsePropertiesChanged(sig busctlSignal) (Event, bool) {
	// The payload data is [interfaceName, changedProperties, invalidated].
	var data []json.RawMessage
	if err := json.Unmarshal(sig.Payload.Data, &data); err != nil || len(data) < 2 {
		return Event{}, false
	}

	var iface string
	if err := json.Unmarshal(data[0], &iface); err != nil {
		return Event{}, false
	}

	if iface != "org.freedesktop.systemd1.Unit" {
		return Event{}, false
	}

	props, err := parseChangedProperties(data[1])
	if err != nil {
		return Event{}, false
	}

	activeState, ok := props["ActiveState"]
	if !ok {
		return Event{}, false
	}

	ev := Event{
		Node:      m.node,
		Container: m.container,
		Time:      time.UnixMicro(sig.TimestampRealtime),
	}

	switch activeState {
	case "active":
		ev.Kind = EventUnitActive
	case "failed":
		ev.Kind = EventUnitFailed
	default:
		return Event{}, false
	}

	unit := unitNameFromPath(sig.Path)
	ev.Unit = unit
	ev.Detail = "ActiveState=" + activeState

	return ev, true
}

// parseChangedProperties extracts string-typed property values from the
// D-Bus PropertiesChanged changed_properties map. The JSON structure is:
//
//	{"PropertyName": {"type": "s", "data": "value"}, ...}
func parseChangedProperties(raw json.RawMessage) (map[string]string, error) {
	var props map[string]struct {
		Type string `json:"type"`
		Data json.RawMessage
	}
	if err := json.Unmarshal(raw, &props); err != nil {
		return nil, err
	}

	result := make(map[string]string, len(props))
	for k, v := range props {
		if v.Type != "s" {
			continue
		}
		var s string
		if err := json.Unmarshal(v.Data, &s); err != nil {
			continue
		}
		result[k] = s
	}
	return result, nil
}

// unitNameFromPath extracts a systemd unit name from a D-Bus object path.
// For example, /org/freedesktop/systemd1/unit/munge_2eservice becomes munge.service.
func unitNameFromPath(path string) string {
	encoded, ok := strings.CutPrefix(path, "/org/freedesktop/systemd1/unit/")
	if !ok {
		return ""
	}
	return dbusPathUnescape(encoded)
}

// dbusPathUnescape reverses systemd's D-Bus path escaping.
// Underscores followed by two hex digits are replaced with the corresponding byte.
// For example, _2e becomes '.', _2d becomes '-'.
func dbusPathUnescape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '_' && i+2 < len(s) {
			if c, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				b.WriteByte(byte(c))
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// SystemdMonitorArgs returns the docker CLI arguments for streaming
// systemd unit state changes from inside a container.
func SystemdMonitorArgs(container docker.ContainerName) []string {
	return []string{
		"exec", string(container),
		"busctl", "monitor",
		"--watch-bind=yes",
		"--json=short",
		"--match", "type=signal,interface=org.freedesktop.DBus.Properties,member=PropertiesChanged,path_namespace=/org/freedesktop/systemd1",
		"org.freedesktop.systemd1",
	}
}
