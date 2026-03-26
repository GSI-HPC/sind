// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"testing"

	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
)

// --- Container logs ---

func TestLogs_Container(t *testing.T) {
	args := ContainerLogArgs(mesh.DefaultRealm, "controller", "dev", false)

	assert.Equal(t, []string{
		"logs", "sind-dev-controller",
	}, args)
}

func TestLogs_ContainerFollow(t *testing.T) {
	args := ContainerLogArgs(mesh.DefaultRealm, "worker-0", "dev", true)

	assert.Equal(t, []string{
		"logs", "--follow", "sind-dev-worker-0",
	}, args)
}

// --- Service logs ---

func TestLogs_Service(t *testing.T) {
	args := ServiceLogArgs(mesh.DefaultRealm, "controller", "dev", "slurmctld", false)

	assert.Equal(t, []string{
		"exec", "sind-dev-controller",
		"journalctl", "-u", "slurmctld",
	}, args)
}

func TestLogs_ServiceFollow(t *testing.T) {
	args := ServiceLogArgs(mesh.DefaultRealm, "worker-0", "dev", "slurmd", true)

	assert.Equal(t, []string{
		"exec", "sind-dev-worker-0",
		"journalctl", "-u", "slurmd", "--follow",
	}, args)
}
