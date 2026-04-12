// SPDX-License-Identifier: LGPL-3.0-or-later

package monitor

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
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

// Run reads from r (the stdout of docker exec CONTAINER busctl monitor ...)
// and sends parsed events to ch. It blocks on scanner.Scan(), which is
// not interruptible by context cancellation alone — to unblock Run on
// ctx.Done(), the caller must also close r (e.g. by killing the
// underlying process or closing the stdout pipe). The caller is
// responsible for closing ch after Run returns.
func (m *SystemdMonitor) Run(ctx context.Context, r io.Reader, ch chan<- Event) error {
	log := sindlog.From(ctx)
	scanner := bufio.NewScanner(r)
	// busctl JSON lines can be very long.
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)

	for scanner.Scan() {
		var sig busctlSignal
		if err := json.Unmarshal(scanner.Bytes(), &sig); err != nil {
			log.Log(ctx, sindlog.LevelTrace, "systemd monitor: skipping unparseable line", "err", err)
			continue
		}

		if sig.Type != "signal" || sig.Member != "PropertiesChanged" {
			continue
		}

		ev, ok := m.parsePropertiesChanged(sig)
		if !ok {
			continue
		}

		select {
		case ch <- ev:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return scanner.Err()
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
	const prefix = "/org/freedesktop/systemd1/unit/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	encoded := strings.TrimPrefix(path, prefix)
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
			hi := unhex(s[i+1])
			lo := unhex(s[i+2])
			if hi >= 0 && lo >= 0 {
				b.WriteByte(byte(hi<<4 | lo))
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func unhex(c byte) int {
	switch {
	case '0' <= c && c <= '9':
		return int(c - '0')
	case 'a' <= c && c <= 'f':
		return int(c - 'a' + 10)
	case 'A' <= c && c <= 'F':
		return int(c - 'A' + 10)
	default:
		return -1
	}
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
