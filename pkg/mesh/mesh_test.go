// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureMeshNetwork_Creates(t *testing.T) {
	const networkID = "6f02052f0a95e0134b3f284b793c63803306b04225f9dc2b40cf48975a2e743b"

	var m docker.MockExecutor
	// NetworkExists → not found (exit code 1)
	m.AddResult("", "Error: No such network: sind-mesh\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	// CreateNetwork → success
	m.AddResult(networkID+"\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureMeshNetwork(context.Background())
	require.NoError(t, err)

	require.Len(t, m.Calls, 2)
	assert.Equal(t, []string{"network", "inspect", string(cluster.MeshNetworkName)}, m.Calls[0].Args)
	assert.Equal(t, []string{"network", "create", string(cluster.MeshNetworkName)}, m.Calls[1].Args)
}

func TestEnsureMeshNetwork_AlreadyExists(t *testing.T) {
	var m docker.MockExecutor
	// NetworkExists → found
	m.AddResult("[{}]\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureMeshNetwork(context.Background())
	require.NoError(t, err)

	// Only inspect, no create
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "inspect", string(cluster.MeshNetworkName)}, m.Calls[0].Args)
}

func TestEnsureMeshNetwork_InspectError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureMeshNetwork(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checking mesh network")
}

func TestEnsureMeshNetwork_CreateError(t *testing.T) {
	var m docker.MockExecutor
	// NetworkExists → not found
	m.AddResult("", "Error: No such network: sind-mesh\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	// CreateNetwork → error
	m.AddResult("", "Error: permission denied\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureMeshNetwork(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating mesh network")
}

func TestEnsureDNS_Creates(t *testing.T) {
	const containerID = "abc123"

	var m docker.MockExecutor
	// ContainerExists → not found
	m.AddResult("", "Error: No such container: sind-dns\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	// CreateContainer → success
	m.AddResult(containerID+"\n", "", nil)
	// CopyToContainer → success
	m.AddResult("", "", nil)
	// StartContainer → success
	m.AddResult("sind-dns\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureDNS(context.Background())
	require.NoError(t, err)

	require.Len(t, m.Calls, 4)
	// 1. Check existence
	assert.Equal(t, []string{"container", "inspect", string(cluster.DNSContainerName)}, m.Calls[0].Args)
	// 2. Create container
	assert.Equal(t, []string{
		"create",
		"--name", string(cluster.DNSContainerName),
		"--network", string(cluster.MeshNetworkName),
		DNSImage,
	}, m.Calls[1].Args)
	// 3. Copy config files
	assert.Equal(t, []string{"cp", "-", string(cluster.DNSContainerName) + ":/"}, m.Calls[2].Args)
	assert.NotEmpty(t, m.Calls[2].Stdin)
	// 4. Start container
	assert.Equal(t, []string{"start", string(cluster.DNSContainerName)}, m.Calls[3].Args)
}

func TestEnsureDNS_AlreadyExists(t *testing.T) {
	var m docker.MockExecutor
	// ContainerExists → found
	m.AddResult("[{}]\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureDNS(context.Background())
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"container", "inspect", string(cluster.DNSContainerName)}, m.Calls[0].Args)
}

func TestEnsureDNS_InspectError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureDNS(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checking DNS container")
}

func TestEnsureDNS_CreateError(t *testing.T) {
	var m docker.MockExecutor
	// ContainerExists → not found
	m.AddResult("", "Error: No such container: sind-dns\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	// CreateContainer → error
	m.AddResult("", "Error: pull access denied\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureDNS(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating DNS container")
}

func TestEnsureDNS_CopyError(t *testing.T) {
	var m docker.MockExecutor
	// ContainerExists → not found
	m.AddResult("", "Error: No such container: sind-dns\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	// CreateContainer → success
	m.AddResult("abc123\n", "", nil)
	// CopyToContainer → error
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureDNS(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing DNS configuration")
}

func TestEnsureDNS_StartError(t *testing.T) {
	var m docker.MockExecutor
	// ContainerExists → not found
	m.AddResult("", "Error: No such container: sind-dns\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	// CreateContainer → success
	m.AddResult("abc123\n", "", nil)
	// CopyToContainer → success
	m.AddResult("", "", nil)
	// StartContainer → error
	m.AddResult("", "Error: cannot start\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureDNS(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "starting DNS container")
}

// exitCode1 runs a command that exits with code 1 and returns its ProcessState.
func exitCode1(t *testing.T) *os.ProcessState {
	t.Helper()
	cmd := exec.Command("sh", "-c", "exit 1")
	err := cmd.Run()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	return exitErr.ProcessState
}
