// SPDX-License-Identifier: LGPL-3.0-or-later

package monitor

import (
	"testing"
	"time"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
)

func TestEventFields(t *testing.T) {
	ev := Event{
		Kind:      EventContainerStart,
		Node:      "controller",
		Container: docker.ContainerName("sind-dev-controller"),
		Time:      time.Unix(1234567890, 0),
		Detail:    "started",
	}

	assert.Equal(t, EventContainerStart, ev.Kind)
	assert.Equal(t, "controller", ev.Node)
	assert.Equal(t, docker.ContainerName("sind-dev-controller"), ev.Container)
	assert.Empty(t, ev.Unit)
	assert.NoError(t, ev.Err)
}

func TestNodeTarget(t *testing.T) {
	nt := NodeTarget{
		ShortName: "worker-0",
		Container: docker.ContainerName("sind-dev-worker-0"),
	}
	assert.Equal(t, "worker-0", nt.ShortName)
}
