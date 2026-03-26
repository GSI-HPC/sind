// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Lifecycle ---

func TestMeshLifecycle(t *testing.T) {
	c, rec := newTestClient(t)
	ctx := t.Context()
	mgr := NewManager(c)

	if !rec.IsIntegration() {
		// EnsureMesh: create network, DNS, SSH volume, SSH container
		rec.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)}) // network exists → no
		rec.AddResult("net-id\n", "", nil)                                        // create network
		rec.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)}) // DNS exists → no
		rec.AddResult("dns-id\n", "", nil)                                        // create DNS
		rec.AddResult("", "", nil)                                                // copy Corefile
		rec.AddResult("sind-dns\n", "", nil)                                      // start DNS
		rec.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)}) // SSH vol exists → no
		rec.AddResult("sind-ssh-config\n", "", nil)                               // create SSH vol
		rec.AddResult("keygen-id\n", "", nil)                                     // create keygen container
		rec.AddResult("", "", nil)                                                // copy keygen script
		rec.AddResult("", "", nil)                                                // remove keygen container
		rec.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)}) // SSH exists → no
		rec.AddResult("ssh-id\n", "", nil)                                        // create SSH
		rec.AddResult("sind-ssh\n", "", nil)                                      // start SSH

		// Verify: network, DNS, SSH volume, SSH container exist
		rec.AddResult("[{}]\n", "", nil) // network exists → yes
		rec.AddResult("[{}]\n", "", nil) // DNS exists → yes
		rec.AddResult("[{}]\n", "", nil) // SSH vol exists → yes
		rec.AddResult("[{}]\n", "", nil) // SSH exists → yes

		// EnsureMesh again (idempotent): all exist
		rec.AddResult("[{}]\n", "", nil) // network
		rec.AddResult("[{}]\n", "", nil) // DNS
		rec.AddResult("[{}]\n", "", nil) // SSH vol
		rec.AddResult("[{}]\n", "", nil) // SSH

		// CleanupMesh: remove SSH, DNS, network, volume
		rec.AddResult("[{}]\n", "", nil)            // SSH exists
		rec.AddResult("sind-ssh\n", "", nil)        // stop SSH
		rec.AddResult("sind-ssh\n", "", nil)        // rm SSH
		rec.AddResult("[{}]\n", "", nil)            // DNS exists
		rec.AddResult("sind-dns\n", "", nil)        // stop DNS
		rec.AddResult("sind-dns\n", "", nil)        // rm DNS
		rec.AddResult("[{}]\n", "", nil)            // network exists
		rec.AddResult("sind-mesh\n", "", nil)       // rm network
		rec.AddResult("[{}]\n", "", nil)            // SSH vol exists
		rec.AddResult("sind-ssh-config\n", "", nil) // rm SSH vol

		// Verify: all gone
		rec.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)}) // network
		rec.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)}) // DNS
		rec.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)}) // SSH
	}
	t.Cleanup(func() { _ = mgr.CleanupMesh(context.Background()) })

	// EnsureMesh creates all resources.
	err := mgr.EnsureMesh(ctx)
	require.NoError(t, err)

	// Verify resources exist.
	exists, err := c.NetworkExists(ctx, NetworkName)
	require.NoError(t, err)
	assert.True(t, exists, "mesh network")

	exists, err = c.ContainerExists(ctx, DNSContainerName)
	require.NoError(t, err)
	assert.True(t, exists, "DNS container")

	exists, err = c.VolumeExists(ctx, SSHVolumeName)
	require.NoError(t, err)
	assert.True(t, exists, "SSH volume")

	exists, err = c.ContainerExists(ctx, SSHContainerName)
	require.NoError(t, err)
	assert.True(t, exists, "SSH container")

	// EnsureMesh is idempotent.
	err = mgr.EnsureMesh(ctx)
	require.NoError(t, err)

	// CleanupMesh removes everything.
	err = mgr.CleanupMesh(ctx)
	require.NoError(t, err)

	// Verify resources gone.
	exists, err = c.NetworkExists(ctx, NetworkName)
	require.NoError(t, err)
	assert.False(t, exists, "mesh network should be gone")

	exists, err = c.ContainerExists(ctx, DNSContainerName)
	require.NoError(t, err)
	assert.False(t, exists, "DNS container should be gone")

	exists, err = c.ContainerExists(ctx, SSHContainerName)
	require.NoError(t, err)
	assert.False(t, exists, "SSH container should be gone")

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestDNSRecordLifecycle(t *testing.T) {
	c, rec := newTestClient(t)
	ctx := t.Context()
	mgr := NewManager(c)

	if !rec.IsIntegration() {
		// EnsureMesh
		rec.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)}) // network exists → no
		rec.AddResult("net-id\n", "", nil)                                        // create network
		rec.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)}) // DNS exists → no
		rec.AddResult("dns-id\n", "", nil)                                        // create DNS
		rec.AddResult("", "", nil)                                                // copy Corefile
		rec.AddResult("sind-dns\n", "", nil)                                      // start DNS
		rec.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)}) // SSH vol exists → no
		rec.AddResult("sind-ssh-config\n", "", nil)                               // create SSH vol
		rec.AddResult("keygen-id\n", "", nil)                                     // create keygen container
		rec.AddResult("", "", nil)                                                // copy keygen script
		rec.AddResult("", "", nil)                                                // remove keygen container
		rec.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)}) // SSH exists → no
		rec.AddResult("ssh-id\n", "", nil)                                        // create SSH
		rec.AddResult("sind-ssh\n", "", nil)                                      // start SSH

		// AddDNSRecord "a": read → write → signal
		rec.AddResult(corefileTar(t, nil), "", nil)
		rec.AddResult("", "", nil)
		rec.AddResult("", "", nil)

		// AddDNSRecord "b": read → write → signal
		rec.AddResult(corefileTar(t, []string{"172.18.0.2 a.test.sind.local"}), "", nil)
		rec.AddResult("", "", nil)
		rec.AddResult("", "", nil)

		// RemoveDNSRecord "a": read → write → signal
		rec.AddResult(corefileTar(t, []string{"172.18.0.2 a.test.sind.local", "172.18.0.3 b.test.sind.local"}), "", nil)
		rec.AddResult("", "", nil)
		rec.AddResult("", "", nil)

		// CleanupMesh
		rec.AddResult("[{}]\n", "", nil)            // SSH exists
		rec.AddResult("sind-ssh\n", "", nil)        // stop SSH
		rec.AddResult("sind-ssh\n", "", nil)        // rm SSH
		rec.AddResult("[{}]\n", "", nil)            // DNS exists
		rec.AddResult("sind-dns\n", "", nil)        // stop DNS
		rec.AddResult("sind-dns\n", "", nil)        // rm DNS
		rec.AddResult("[{}]\n", "", nil)            // network exists
		rec.AddResult("sind-mesh\n", "", nil)       // rm network
		rec.AddResult("[{}]\n", "", nil)            // SSH vol exists
		rec.AddResult("sind-ssh-config\n", "", nil) // rm SSH vol
	}
	t.Cleanup(func() { _ = mgr.CleanupMesh(context.Background()) })

	err := mgr.EnsureMesh(ctx)
	require.NoError(t, err)

	// Add two records.
	err = mgr.AddDNSRecord(ctx, "a.test.sind.local", "172.18.0.2")
	require.NoError(t, err)

	err = mgr.AddDNSRecord(ctx, "b.test.sind.local", "172.18.0.3")
	require.NoError(t, err)

	// Remove first record.
	err = mgr.RemoveDNSRecord(ctx, "a.test.sind.local")
	require.NoError(t, err)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

// --- EnsureMesh ---

func TestEnsureMesh(t *testing.T) {
	var m docker.MockExecutor
	// EnsureMeshNetwork: NetworkExists → not found, CreateNetwork → success
	m.AddResult("", "Error: No such network: sind-mesh\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	m.AddResult("net-id\n", "", nil)
	// EnsureDNS: ContainerExists → not found, CreateContainer, CopyToContainer, StartContainer
	m.AddResult("", "Error: No such container: sind-dns\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	m.AddResult("dns-id\n", "", nil)
	m.AddResult("", "", nil)
	m.AddResult("sind-dns\n", "", nil)
	// EnsureSSHVolume: VolumeExists → not found, CreateVolume, CreateContainer, CopyToContainer, RemoveContainer
	m.AddResult("", "Error: No such volume: sind-ssh-config\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	m.AddResult("sind-ssh-config\n", "", nil)
	m.AddResult("keygen-id\n", "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	// EnsureSSH: ContainerExists → not found, CreateContainer, StartContainer
	m.AddResult("", "Error: No such container: sind-ssh\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	m.AddResult("ssh-id\n", "", nil)
	m.AddResult("sind-ssh\n", "", nil)

	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureMesh(t.Context())
	require.NoError(t, err)
}

func TestEnsureMesh_AllExist(t *testing.T) {
	var m docker.MockExecutor
	// All four exist checks return success.
	m.AddResult("[{}]\n", "", nil) // NetworkExists
	m.AddResult("[{}]\n", "", nil) // ContainerExists (DNS)
	m.AddResult("[{}]\n", "", nil) // VolumeExists (SSH)
	m.AddResult("[{}]\n", "", nil) // ContainerExists (SSH)

	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureMesh(t.Context())
	require.NoError(t, err)
	assert.Len(t, m.Calls, 4)
}

func TestEnsureMesh_NetworkError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureMesh(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mesh network")
}

func TestEnsureMesh_DNSError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("[{}]\n", "", nil) // network exists
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureMesh(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "DNS container")
}

func TestEnsureMesh_SSHVolumeError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("[{}]\n", "", nil) // network exists
	m.AddResult("[{}]\n", "", nil) // DNS exists
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureMesh(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SSH volume")
}

func TestEnsureMesh_SSHContainerError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("[{}]\n", "", nil) // network exists
	m.AddResult("[{}]\n", "", nil) // DNS exists
	m.AddResult("[{}]\n", "", nil) // SSH volume exists
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureMesh(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SSH container")
}

// --- CleanupMesh ---

func TestCleanupMesh(t *testing.T) {
	var m docker.MockExecutor
	// removeContainerIfExists(SSH): exists, stop, remove
	m.AddResult("[{}]\n", "", nil)
	m.AddResult("sind-ssh\n", "", nil)
	m.AddResult("sind-ssh\n", "", nil)
	// removeContainerIfExists(DNS): exists, stop, remove
	m.AddResult("[{}]\n", "", nil)
	m.AddResult("sind-dns\n", "", nil)
	m.AddResult("sind-dns\n", "", nil)
	// removeNetworkIfExists: exists, remove
	m.AddResult("[{}]\n", "", nil)
	m.AddResult("sind-mesh\n", "", nil)
	// removeVolumeIfExists: exists, remove
	m.AddResult("[{}]\n", "", nil)
	m.AddResult("sind-ssh-config\n", "", nil)

	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.CleanupMesh(t.Context())
	require.NoError(t, err)

	// Verify order: SSH container, DNS container, network, volume.
	assert.Equal(t, []string{"container", "inspect", string(SSHContainerName)}, m.Calls[0].Args)
	assert.Equal(t, []string{"stop", string(SSHContainerName)}, m.Calls[1].Args)
	assert.Equal(t, []string{"rm", string(SSHContainerName)}, m.Calls[2].Args)
	assert.Equal(t, []string{"container", "inspect", string(DNSContainerName)}, m.Calls[3].Args)
	assert.Equal(t, []string{"stop", string(DNSContainerName)}, m.Calls[4].Args)
	assert.Equal(t, []string{"rm", string(DNSContainerName)}, m.Calls[5].Args)
	assert.Equal(t, []string{"network", "inspect", string(NetworkName)}, m.Calls[6].Args)
	assert.Equal(t, []string{"network", "rm", string(NetworkName)}, m.Calls[7].Args)
	assert.Equal(t, []string{"volume", "inspect", string(SSHVolumeName)}, m.Calls[8].Args)
	assert.Equal(t, []string{"volume", "rm", string(SSHVolumeName)}, m.Calls[9].Args)
}

func TestCleanupMesh_NoneExist(t *testing.T) {
	var m docker.MockExecutor
	// All four exist checks return not found.
	m.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)})
	m.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)})
	m.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)})
	m.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)})

	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.CleanupMesh(t.Context())
	require.NoError(t, err)
	assert.Len(t, m.Calls, 4) // only exist checks, no removes
}

func TestCleanupMesh_SSHContainerError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.CleanupMesh(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "removing SSH container")
}

func TestCleanupMesh_DNSContainerError(t *testing.T) {
	var m docker.MockExecutor
	// SSH container doesn't exist
	m.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)})
	// DNS container check fails
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.CleanupMesh(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "removing DNS container")
}

func TestCleanupMesh_NetworkError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)}) // SSH
	m.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)}) // DNS
	m.AddResult("", "", fmt.Errorf("connection refused"))                   // network
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.CleanupMesh(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "removing mesh network")
}

func TestCleanupMesh_RemoveContainerError(t *testing.T) {
	var m docker.MockExecutor
	// SSH container: exists, stop (best-effort), remove fails
	m.AddResult("[{}]\n", "", nil)
	m.AddResult("sind-ssh\n", "", nil)
	m.AddResult("", "Error: removal in progress\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.CleanupMesh(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "removing SSH container")
}

func TestCleanupMesh_VolumeError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)}) // SSH
	m.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)}) // DNS
	m.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)}) // network
	m.AddResult("", "", fmt.Errorf("connection refused"))                   // volume
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.CleanupMesh(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "removing SSH volume")
}

// --- EnsureMeshNetwork ---

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

	err := mgr.EnsureMeshNetwork(t.Context())
	require.NoError(t, err)

	require.Len(t, m.Calls, 2)
	assert.Equal(t, []string{"network", "inspect", string(NetworkName)}, m.Calls[0].Args)
	assert.Equal(t, []string{"network", "create", string(NetworkName)}, m.Calls[1].Args)
}

func TestEnsureMeshNetwork_AlreadyExists(t *testing.T) {
	var m docker.MockExecutor
	// NetworkExists → found
	m.AddResult("[{}]\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureMeshNetwork(t.Context())
	require.NoError(t, err)

	// Only inspect, no create
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "inspect", string(NetworkName)}, m.Calls[0].Args)
}

func TestEnsureMeshNetwork_InspectError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureMeshNetwork(t.Context())
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

	err := mgr.EnsureMeshNetwork(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating mesh network")
}

// --- EnsureDNS ---

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

	err := mgr.EnsureDNS(t.Context())
	require.NoError(t, err)

	require.Len(t, m.Calls, 4)
	assert.Equal(t, []string{"container", "inspect", string(DNSContainerName)}, m.Calls[0].Args)
	assert.Equal(t, []string{
		"create",
		"--name", string(DNSContainerName),
		"--network", string(NetworkName),
		DNSImage,
	}, m.Calls[1].Args)
	assert.Equal(t, []string{"cp", "-", string(DNSContainerName) + ":/"}, m.Calls[2].Args)
	assert.Equal(t, []string{"start", string(DNSContainerName)}, m.Calls[3].Args)

	// Verify initial Corefile has empty hosts block
	corefile := extractTarFile(t, m.Calls[2].Stdin, "Corefile")
	assert.Contains(t, corefile, "hosts {")
	assert.Contains(t, corefile, "fallthrough")
	assert.NotContains(t, corefile, "sind.local\n")
}

func TestEnsureDNS_AlreadyExists(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("[{}]\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureDNS(t.Context())
	require.NoError(t, err)
	require.Len(t, m.Calls, 1)
}

func TestEnsureDNS_InspectError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureDNS(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checking DNS container")
}

func TestEnsureDNS_CreateError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error: No such container: sind-dns\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	m.AddResult("", "Error: pull access denied\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureDNS(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating DNS container")
}

func TestEnsureDNS_CopyError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error: No such container: sind-dns\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	m.AddResult("abc123\n", "", nil)
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureDNS(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing DNS configuration")
}

func TestEnsureDNS_StartError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error: No such container: sind-dns\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	m.AddResult("abc123\n", "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "Error: cannot start\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureDNS(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "starting DNS container")
}

// --- AddDNSRecord ---

func TestAddDNSRecord_Empty(t *testing.T) {
	var m docker.MockExecutor
	// CopyFromContainer → Corefile with empty hosts block
	m.AddResult(corefileTar(t, nil), "", nil)
	// CopyToContainer → success
	m.AddResult("", "", nil)
	// SignalContainer → success
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.AddDNSRecord(t.Context(), "controller.dev.sind.local", "172.18.0.2")
	require.NoError(t, err)

	require.Len(t, m.Calls, 3)
	// Verify read
	assert.Equal(t, []string{"cp", string(DNSContainerName) + ":/Corefile", "-"}, m.Calls[0].Args)
	// Verify written Corefile contains the record
	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.Contains(t, corefile, "172.18.0.2 controller.dev.sind.local")
	// Verify SIGHUP
	assert.Equal(t, []string{"kill", "-s", "HUP", string(DNSContainerName)}, m.Calls[2].Args)
}

func TestAddDNSRecord_Appends(t *testing.T) {
	existing := []string{"172.18.0.2 controller.dev.sind.local"}

	var m docker.MockExecutor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.AddDNSRecord(t.Context(), "compute-0.dev.sind.local", "172.18.0.3")
	require.NoError(t, err)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.Contains(t, corefile, "172.18.0.2 controller.dev.sind.local")
	assert.Contains(t, corefile, "172.18.0.3 compute-0.dev.sind.local")
}

func TestAddDNSRecord_ReadError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.AddDNSRecord(t.Context(), "controller.dev.sind.local", "172.18.0.2")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading DNS Corefile")
}

func TestAddDNSRecord_WriteError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult(corefileTar(t, nil), "", nil)
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.AddDNSRecord(t.Context(), "controller.dev.sind.local", "172.18.0.2")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing DNS Corefile")
}

func TestAddDNSRecord_SignalError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult(corefileTar(t, nil), "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.AddDNSRecord(t.Context(), "controller.dev.sind.local", "172.18.0.2")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reloading DNS")
}

// --- RemoveDNSRecord ---

func TestRemoveDNSRecord(t *testing.T) {
	existing := []string{
		"172.18.0.2 controller.dev.sind.local",
		"172.18.0.3 compute-0.dev.sind.local",
	}

	var m docker.MockExecutor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.RemoveDNSRecord(t.Context(), "controller.dev.sind.local")
	require.NoError(t, err)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.NotContains(t, corefile, "controller.dev.sind.local")
	assert.Contains(t, corefile, "172.18.0.3 compute-0.dev.sind.local")
}

func TestRemoveDNSRecord_LastEntry(t *testing.T) {
	existing := []string{"172.18.0.2 controller.dev.sind.local"}

	var m docker.MockExecutor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.RemoveDNSRecord(t.Context(), "controller.dev.sind.local")
	require.NoError(t, err)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.NotContains(t, corefile, "controller.dev.sind.local")
	// Should still have valid Corefile structure
	assert.Contains(t, corefile, "hosts {")
	assert.Contains(t, corefile, "fallthrough")
}

func TestRemoveDNSRecord_NotFound(t *testing.T) {
	existing := []string{"172.18.0.2 controller.dev.sind.local"}

	var m docker.MockExecutor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.RemoveDNSRecord(t.Context(), "compute-0.dev.sind.local")
	require.NoError(t, err)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.Contains(t, corefile, "172.18.0.2 controller.dev.sind.local")
}

func TestRemoveDNSRecord_SignalError(t *testing.T) {
	existing := []string{"172.18.0.2 controller.dev.sind.local"}

	var m docker.MockExecutor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.RemoveDNSRecord(t.Context(), "controller.dev.sind.local")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reloading DNS")
}

func TestRemoveDNSRecord_DuplicateHostnames(t *testing.T) {
	existing := []string{
		"172.18.0.2 controller.dev.sind.local",
		"172.18.0.5 controller.dev.sind.local",
		"172.18.0.3 compute-0.dev.sind.local",
	}

	var m docker.MockExecutor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.RemoveDNSRecord(t.Context(), "controller.dev.sind.local")
	require.NoError(t, err)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.NotContains(t, corefile, "controller.dev.sind.local")
	assert.Contains(t, corefile, "172.18.0.3 compute-0.dev.sind.local")
}

func TestRemoveDNSRecord_ReadError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.RemoveDNSRecord(t.Context(), "controller.dev.sind.local")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading DNS Corefile")
}

func TestRemoveDNSRecord_WriteError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult(corefileTar(t, []string{"172.18.0.2 x"}), "", nil)
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.RemoveDNSRecord(t.Context(), "x")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing DNS Corefile")
}

// --- generateCorefile / parseEntries ---

func TestGenerateCorefile_Empty(t *testing.T) {
	cf := generateCorefile(nil)
	assert.Contains(t, cf, "sind.local:53")
	assert.Contains(t, cf, "hosts {")
	assert.Contains(t, cf, "fallthrough")
	assert.Contains(t, cf, "forward . /etc/resolv.conf")
}

func TestGenerateCorefile_WithEntries(t *testing.T) {
	entries := []string{
		"172.18.0.2 controller.dev.sind.local",
		"172.18.0.3 compute-0.dev.sind.local",
	}
	cf := generateCorefile(entries)
	assert.Contains(t, cf, "        172.18.0.2 controller.dev.sind.local\n")
	assert.Contains(t, cf, "        172.18.0.3 compute-0.dev.sind.local\n")
}

func TestParseEntries_Empty(t *testing.T) {
	cf := generateCorefile(nil)
	entries := parseEntries(cf)
	assert.Empty(t, entries)
}

func TestParseEntries_Roundtrip(t *testing.T) {
	original := []string{
		"172.18.0.2 controller.dev.sind.local",
		"172.18.0.3 compute-0.dev.sind.local",
	}
	cf := generateCorefile(original)
	parsed := parseEntries(cf)
	assert.Equal(t, original, parsed)
}

func TestParseEntries_NoHostsBlock(t *testing.T) {
	entries := parseEntries(".:53 {\n    forward . /etc/resolv.conf\n}\n")
	assert.Empty(t, entries)
}

// --- test helpers ---

// corefileTar builds a tar archive containing a Corefile with the given entries,
// matching the format returned by docker cp.
func corefileTar(t *testing.T, entries []string) string {
	t.Helper()
	content := []byte(generateCorefile(entries))
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.WriteHeader(&tar.Header{Name: "Corefile", Size: int64(len(content)), Mode: 0644})
	tw.Write(content)
	tw.Close()
	return buf.String()
}

// extractTarFile reads a named file from a tar archive captured by MockExecutor.
func extractTarFile(t *testing.T, tarData, name string) string {
	t.Helper()
	tr := tar.NewReader(strings.NewReader(tarData))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			t.Fatalf("file %q not found in tar", name)
		}
		require.NoError(t, err)
		if hdr.Name == name {
			data, err := io.ReadAll(tr)
			require.NoError(t, err)
			return string(data)
		}
	}
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
