// SPDX-License-Identifier: LGPL-3.0-or-later

// Package monitor provides event-driven monitoring of Docker containers
// and systemd services inside them. It complements the poll-based probes
// in pkg/probe by pushing state transitions through channels.
package monitor

import (
	"time"

	"github.com/GSI-HPC/sind/pkg/docker"
)

// EventKind identifies the category of a monitored event.
type EventKind string

// EventContainerStart indicates a container has started.
const EventContainerStart EventKind = "container.start"

// EventContainerDie indicates a container has exited.
const EventContainerDie EventKind = "container.die"

// EventContainerOOM indicates a container was killed by the OOM killer.
const EventContainerOOM EventKind = "container.oom"

// EventContainerPause indicates a container was paused.
const EventContainerPause EventKind = "container.pause"

// EventContainerUnpause indicates a container was unpaused.
const EventContainerUnpause EventKind = "container.unpause"

// EventUnitActive indicates a systemd unit reached ActiveState=active.
const EventUnitActive EventKind = "unit.active"

// EventUnitFailed indicates a systemd unit reached ActiveState=failed.
const EventUnitFailed EventKind = "unit.failed"

// EventMonitorError indicates an unrecoverable monitor failure.
const EventMonitorError EventKind = "monitor.error"

// Event represents a single state change observed by a monitor.
type Event struct {
	Kind      EventKind
	Node      string               // short name: "controller", "worker-0"
	Container docker.ContainerName // full container name
	Unit      string               // systemd unit name (empty for container events)
	Time      time.Time
	Detail    string // human-readable detail (exit code, error message, etc.)
	Err       error  // non-nil for EventMonitorError
}

// NodeTarget identifies a node to monitor.
type NodeTarget struct {
	ShortName string
	Container docker.ContainerName
}
