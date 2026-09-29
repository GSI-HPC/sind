// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVolumePlugins(t *testing.T) {
	var m mock.Executor
	m.AddResult("cvmfs:latest\nvieux/sshfs:latest\n", "", nil)
	c := NewClient(&m)

	plugins, err := c.VolumePlugins(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []string{"cvmfs:latest", "vieux/sshfs:latest"}, plugins)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"plugin", "ls",
		"--filter", "capability=volumedriver",
		"--filter", "enabled=true",
		"--format", "{{.Name}}"}, m.Calls[0].Args)
}

func TestVolumePlugins_None(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	plugins, err := c.VolumePlugins(t.Context())
	require.NoError(t, err)
	assert.Empty(t, plugins)
}

func TestVolumePlugins_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Cannot connect", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	_, err := c.VolumePlugins(t.Context())
	assert.Error(t, err)
}
