// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"testing"

	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
)

// --- Container logs ---

func TestLogs_Container(t *testing.T) {
	args := ContainerLogArgs(mesh.DefaultRealm, "controller", "dev", LogOptions{})

	assert.Equal(t, []string{
		"logs", "sind-dev-controller",
	}, args)
}

func TestLogs_ContainerFollow(t *testing.T) {
	args := ContainerLogArgs(mesh.DefaultRealm, "worker-0", "dev", LogOptions{Follow: true})

	assert.Equal(t, []string{
		"logs", "--follow", "sind-dev-worker-0",
	}, args)
}

// --- Service logs ---

func TestLogs_Service(t *testing.T) {
	args := ServiceLogArgs(mesh.DefaultRealm, "controller", "dev", "slurmctld", LogOptions{})

	assert.Equal(t, []string{
		"exec", "sind-dev-controller",
		"journalctl", "-u", "slurmctld",
	}, args)
}

func TestLogs_ServiceFollow(t *testing.T) {
	args := ServiceLogArgs(mesh.DefaultRealm, "worker-0", "dev", "slurmd", LogOptions{Follow: true})

	assert.Equal(t, []string{
		"exec", "sind-dev-worker-0",
		"journalctl", "-u", "slurmd", "--follow",
	}, args)
}

func TestLogs_ContainerTail(t *testing.T) {
	tail := 20
	args := ContainerLogArgs(mesh.DefaultRealm, "controller", "dev", LogOptions{Tail: &tail})

	assert.Equal(t, []string{
		"logs", "--tail", "20", "sind-dev-controller",
	}, args)
}

func TestLogs_ServiceTail(t *testing.T) {
	tail := 0
	args := ServiceLogArgs(mesh.DefaultRealm, "controller", "dev", "slurmctld", LogOptions{Follow: true, Tail: &tail})

	assert.Equal(t, []string{
		"exec", "sind-dev-controller",
		"journalctl", "-u", "slurmctld", "--follow", "-n", "0",
	}, args)
}
