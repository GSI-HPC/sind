// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- DetectCVMFS ---

func TestDetectCVMFS_VolumePlugin(t *testing.T) {
	// docker plugin ls prints managed plugins with their tag; an untagged
	// name is accepted as well.
	for _, name := range []string{"cvmfs:latest", "cvmfs"} {
		t.Run(name, func(t *testing.T) {
			var m mock.Executor
			m.AddResult("vieux/sshfs:latest\n"+name+"\n", "", nil)

			backend, err := DetectCVMFS(t.Context(), docker.NewClient(&m), "img:1")

			require.NoError(t, err)
			assert.Equal(t, config.StorageVolume, backend)
			require.Len(t, m.Calls, 1, "no bind mount probe with the plugin")
			assert.Equal(t, []string{"plugin", "ls"}, m.Calls[0].Args[:2])
		})
	}
}

func TestDetectCVMFS_HostPath(t *testing.T) {
	// A plugin named otherwise, e.g. with another tag, is not what
	// volume-driver=cvmfs resolves to.
	var m mock.Executor
	m.AddResult("cvmfs:v1\n", "", nil) // plugin ls
	m.AddResult("", "", nil)           // bind mount probe

	backend, err := DetectCVMFS(t.Context(), docker.NewClient(&m), "img:1")

	require.NoError(t, err)
	assert.Equal(t, config.StorageHostPath, backend)
	require.Len(t, m.Calls, 2)
	assert.Equal(t, []string{"run", "--rm",
		"--mount", "type=bind,source=/cvmfs,target=/cvmfs,readonly,bind-propagation=rslave",
		"--entrypoint", "true", "img:1"}, m.Calls[1].Args)
}

func TestDetectCVMFS_Unavailable(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil) // plugin ls: none
	m.AddResult("", "docker: Error response from daemon: invalid mount config for type \"bind\": bind source path does not exist: /cvmfs",
		fmt.Errorf("exit status 125"))

	_, err := DetectCVMFS(t.Context(), docker.NewClient(&m), "img:1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage.cvmfs")
	assert.Contains(t, err.Error(), "install CVMFS on the Docker host or the \"cvmfs\" Docker volume plugin")
	assert.Contains(t, err.Error(), "exit status 125")
}

func TestDetectCVMFS_PluginListError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Cannot connect", fmt.Errorf("exit status 1"))

	_, err := DetectCVMFS(t.Context(), docker.NewClient(&m), "img:1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing volume plugins")
	require.Len(t, m.Calls, 1)
}

// --- cvmfsMountArgs, cvmfsSource ---

func TestCVMFSMountArgs(t *testing.T) {
	assert.Equal(t, []string{"--mount", "type=volume,volume-driver=cvmfs,source=cvmfs,target=/cvmfs,readonly"},
		cvmfsMountArgs(config.StorageVolume))
	assert.Equal(t, []string{"--mount", "type=bind,source=/cvmfs,target=/cvmfs,readonly,bind-propagation=rslave"},
		cvmfsMountArgs(config.StorageHostPath))
	assert.Nil(t, cvmfsMountArgs(""))
}

func TestCVMFSSource(t *testing.T) {
	assert.Equal(t, "cvmfs", cvmfsSource(config.StorageVolume))
	assert.Equal(t, "/cvmfs", cvmfsSource(config.StorageHostPath))
}
