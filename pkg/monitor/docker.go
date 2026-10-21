// SPDX-License-Identifier: LGPL-3.0-or-later

package monitor

import (
	"context"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/pkg/docker"
)

// dockerEvent is the JSON structure emitted by docker events --format '{{json .}}'.
type dockerEvent struct {
	Action string `json:"Action"`
	Actor  struct {
		Attributes map[string]string `json:"Attributes"`
	} `json:"Actor"`
	Time int64 `json:"time"`
}

// dockerActionMap maps docker event actions to EventKind values.
var dockerActionMap = map[string]EventKind{
	"start":   EventContainerStart,
	"die":     EventContainerDie,
	"oom":     EventContainerOOM,
	"pause":   EventContainerPause,
	"unpause": EventContainerUnpause,
}

// DockerMonitor reads a docker events JSON stream and emits Events.
type DockerMonitor struct {
	containerPrefix string
}

// maxDockerEventLine is the longest docker events line DockerMonitor reads.
const maxDockerEventLine = 16 << 20

// NewDockerMonitor creates a monitor for the given cluster. The container
// prefix is used to extract short node names from full container names.
func NewDockerMonitor(containerPrefix string) *DockerMonitor {
	return &DockerMonitor{containerPrefix: containerPrefix}
}

// Run reads docker events --format '{{json .}}' from r and sends the
// events of the cluster's containers to ch (see scanEvents).
func (m *DockerMonitor) Run(ctx context.Context, r io.Reader, ch chan<- Event) error {
	// An event line carries every label of the container, and the
	// sind.users and sind.groups labels grow with the cluster's users: a
	// few thousand users exceed bufio's default limit of 64 KiB, which
	// would end the monitor at a node's first event.
	return scanEvents(ctx, r, ch, "docker monitor", maxDockerEventLine, func(de dockerEvent) (Event, bool) {
		kind, ok := dockerActionMap[de.Action]
		if !ok {
			return Event{}, false
		}

		name := de.Actor.Attributes["name"]
		shortName, ok := m.extractShortName(name)
		if !ok {
			return Event{}, false
		}

		ev := Event{
			Kind:      kind,
			Node:      shortName,
			Container: docker.ContainerName(name),
			Time:      time.Unix(de.Time, 0),
		}

		if kind == EventContainerDie {
			ev.Detail = "exitCode=" + de.Actor.Attributes["exitCode"]
		}
		return ev, true
	})
}

// extractShortName extracts the short node name from a full container name
// by stripping the container prefix. Returns false if the name does not
// belong to this cluster.
func (m *DockerMonitor) extractShortName(containerName string) (string, bool) {
	short, ok := strings.CutPrefix(containerName, m.containerPrefix)
	return short, ok && short != ""
}

// DockerEventsArgs returns the docker CLI arguments for streaming container
// events for the given cluster. Only the actions in dockerActionMap are
// asked for: every docker exec of the readiness probes would otherwise
// send exec_create, exec_start and exec_die events to parse and drop.
func DockerEventsArgs(clusterName string) []string {
	args := []string{
		"events",
		"--filter", "type=container",
		"--filter", "label=sind.cluster=" + clusterName,
	}
	for _, action := range slices.Sorted(maps.Keys(dockerActionMap)) {
		args = append(args, "--filter", "event="+action)
	}
	return append(args, "--format", "{{json .}}")
}
