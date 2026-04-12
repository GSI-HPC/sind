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

// NewDockerMonitor creates a monitor for the given cluster. The container
// prefix is used to extract short node names from full container names.
func NewDockerMonitor(containerPrefix string) *DockerMonitor {
	return &DockerMonitor{containerPrefix: containerPrefix}
}

// Run reads from r (the stdout of docker events --format '{{json .}}')
// and sends parsed events to ch. It blocks on scanner.Scan(), which is
// not interruptible by context cancellation alone — to unblock Run on
// ctx.Done(), the caller must also close r (e.g. by killing the
// underlying process or closing the stdout pipe). The caller is
// responsible for closing ch after Run returns.
func (m *DockerMonitor) Run(ctx context.Context, r io.Reader, ch chan<- Event) error {
	log := sindlog.From(ctx)
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		var de dockerEvent
		if err := json.Unmarshal(scanner.Bytes(), &de); err != nil {
			log.Log(ctx, sindlog.LevelTrace, "docker monitor: skipping unparseable line", "err", err)
			continue
		}

		kind, ok := dockerActionMap[de.Action]
		if !ok {
			continue
		}

		name := de.Actor.Attributes["name"]
		shortName, ok := m.extractShortName(name)
		if !ok {
			continue
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

		select {
		case ch <- ev:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return scanner.Err()
}

// extractShortName extracts the short node name from a full container name
// by stripping the container prefix. Returns false if the name does not
// belong to this cluster.
func (m *DockerMonitor) extractShortName(containerName string) (string, bool) {
	if !strings.HasPrefix(containerName, m.containerPrefix) {
		return "", false
	}
	short := strings.TrimPrefix(containerName, m.containerPrefix)
	if short == "" {
		return "", false
	}
	return short, true
}

// DockerEventsArgs returns the docker CLI arguments for streaming container
// events for the given cluster.
func DockerEventsArgs(clusterName string) []string {
	return []string{
		"events",
		"--filter", "type=container",
		"--filter", "label=sind.cluster=" + clusterName,
		"--format", "{{json .}}",
	}
}
