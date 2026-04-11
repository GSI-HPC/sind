// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Lifecycle ---

func TestMeshLifecycle(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	ctx := t.Context()
	mgr := NewManager(c, testutil.Realm("it-mesh"))

	if !rec.IsIntegration() {
		// EnsureMesh: create network, DNS, SSH volume, SSH container
		rec.AddResult("", "Error\n", testutil.ExitCode1(t)) // network exists → no
		rec.AddResult("net-id\n", "", nil)                  // create network
		rec.AddResult("", "Error\n", testutil.ExitCode1(t)) // DNS exists → no
		rec.AddResult("dns-id\n", "", nil)                  // create DNS
		rec.AddResult("", "", nil)                          // copy Corefile
		rec.AddResult("sind-dns\n", "", nil)                // start DNS
		rec.AddResult("", "Error\n", testutil.ExitCode1(t)) // SSH vol exists → no
		rec.AddResult("sind-ssh-config\n", "", nil)         // create SSH vol
		rec.AddResult("keygen-id\n", "", nil)               // create keygen container
		rec.AddResult("", "", nil)                          // copy keygen script
		rec.AddResult("", "", nil)                          // remove keygen container
		rec.AddResult("", "Error\n", testutil.ExitCode1(t)) // SSH exists → no
		rec.AddResult(dnsInspectJSON(), "", nil)            // inspect DNS for IP
		rec.AddResult("ssh-id\n", "", nil)                  // create SSH
		rec.AddResult("sind-ssh\n", "", nil)                // start SSH

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

		// CleanupMesh: remove keygen (gone), SSH, DNS, network, volume
		rec.AddResult("", "Error\n", testutil.ExitCode1(t)) // keygen exists → no
		rec.AddResult("[{}]\n", "", nil)                    // SSH exists
		rec.AddResult("sind-ssh\n", "", nil)                // rm -f SSH
		rec.AddResult("[{}]\n", "", nil)                    // DNS exists
		rec.AddResult("sind-dns\n", "", nil)                // rm -f DNS
		rec.AddResult("[{}]\n", "", nil)                    // network exists
		rec.AddResult("sind-mesh\n", "", nil)               // rm network
		rec.AddResult("[{}]\n", "", nil)                    // SSH vol exists
		rec.AddResult("sind-ssh-config\n", "", nil)         // rm SSH vol

		// Verify: all gone
		rec.AddResult("", "Error\n", testutil.ExitCode1(t)) // network
		rec.AddResult("", "Error\n", testutil.ExitCode1(t)) // DNS
		rec.AddResult("", "Error\n", testutil.ExitCode1(t)) // SSH

	}
	t.Cleanup(func() { _ = mgr.CleanupMesh(context.Background()) })

	// EnsureMesh creates all resources.
	err := mgr.EnsureMesh(ctx)
	require.NoError(t, err)

	// Verify resources exist.
	exists, err := c.NetworkExists(ctx, mgr.NetworkName())
	require.NoError(t, err)
	assert.True(t, exists, "mesh network")

	exists, err = c.ContainerExists(ctx, mgr.DNSContainerName())
	require.NoError(t, err)
	assert.True(t, exists, "DNS container")

	exists, err = c.VolumeExists(ctx, mgr.SSHVolumeName())
	require.NoError(t, err)
	assert.True(t, exists, "SSH volume")

	exists, err = c.ContainerExists(ctx, mgr.SSHContainerName())
	require.NoError(t, err)
	assert.True(t, exists, "SSH container")

	// EnsureMesh is idempotent.
	err = mgr.EnsureMesh(ctx)
	require.NoError(t, err)

	// CleanupMesh removes everything.
	err = mgr.CleanupMesh(ctx)
	require.NoError(t, err)

	// Verify resources gone.
	exists, err = c.NetworkExists(ctx, mgr.NetworkName())
	require.NoError(t, err)
	assert.False(t, exists, "mesh network should be gone")

	exists, err = c.ContainerExists(ctx, mgr.DNSContainerName())
	require.NoError(t, err)
	assert.False(t, exists, "DNS container should be gone")

	exists, err = c.ContainerExists(ctx, mgr.SSHContainerName())
	require.NoError(t, err)
	assert.False(t, exists, "SSH container should be gone")

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestDNSRecordLifecycle(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	ctx := t.Context()
	mgr := NewManager(c, testutil.Realm("it-mesh"))

	if !rec.IsIntegration() {
		// EnsureMesh
		rec.AddResult("", "Error\n", testutil.ExitCode1(t)) // network exists → no
		rec.AddResult("net-id\n", "", nil)                  // create network
		rec.AddResult("", "Error\n", testutil.ExitCode1(t)) // DNS exists → no
		rec.AddResult("dns-id\n", "", nil)                  // create DNS
		rec.AddResult("", "", nil)                          // copy Corefile
		rec.AddResult("sind-dns\n", "", nil)                // start DNS
		rec.AddResult("", "Error\n", testutil.ExitCode1(t)) // SSH vol exists → no
		rec.AddResult("sind-ssh-config\n", "", nil)         // create SSH vol
		rec.AddResult("keygen-id\n", "", nil)               // create keygen container
		rec.AddResult("", "", nil)                          // copy keygen script
		rec.AddResult("", "", nil)                          // remove keygen container
		rec.AddResult("", "Error\n", testutil.ExitCode1(t)) // SSH exists → no
		rec.AddResult(dnsInspectJSON(), "", nil)            // inspect DNS for IP
		rec.AddResult("ssh-id\n", "", nil)                  // create SSH
		rec.AddResult("sind-ssh\n", "", nil)                // start SSH

		// AddDNSRecord "a": read → write → kill → start
		rec.AddResult(corefileTar(t, nil), "", nil)
		rec.AddResult("", "", nil)
		rec.AddResult("sind-dns\n", "", nil)
		rec.AddResult("sind-dns\n", "", nil)

		// AddDNSRecord "b": read → write → kill → start
		rec.AddResult(corefileTar(t, []string{"172.18.0.2 a.test.sind.sind"}), "", nil)
		rec.AddResult("", "", nil)
		rec.AddResult("sind-dns\n", "", nil)
		rec.AddResult("sind-dns\n", "", nil)

		// RemoveDNSRecord "a": read → write → kill → start
		rec.AddResult(corefileTar(t, []string{"172.18.0.2 a.test.sind.sind", "172.18.0.3 b.test.sind.sind"}), "", nil)
		rec.AddResult("", "", nil)
		rec.AddResult("sind-dns\n", "", nil)
		rec.AddResult("sind-dns\n", "", nil)

		// CleanupMesh
		rec.AddResult("", "Error\n", testutil.ExitCode1(t)) // keygen exists → no
		rec.AddResult("[{}]\n", "", nil)                    // SSH exists
		rec.AddResult("sind-ssh\n", "", nil)                // rm -f SSH
		rec.AddResult("[{}]\n", "", nil)                    // DNS exists
		rec.AddResult("sind-dns\n", "", nil)                // rm -f DNS
		rec.AddResult("[{}]\n", "", nil)                    // network exists
		rec.AddResult("sind-mesh\n", "", nil)               // rm network
		rec.AddResult("[{}]\n", "", nil)                    // SSH vol exists
		rec.AddResult("sind-ssh-config\n", "", nil)         // rm SSH vol
	}
	t.Cleanup(func() { _ = mgr.CleanupMesh(context.Background()) })

	err := mgr.EnsureMesh(ctx)
	require.NoError(t, err)

	// Add two records.
	err = mgr.AddDNSRecord(ctx, "a.test.sind.sind", "172.18.0.2")
	require.NoError(t, err)

	err = mgr.AddDNSRecord(ctx, "b.test.sind.sind", "172.18.0.3")
	require.NoError(t, err)

	// Remove first record.
	err = mgr.RemoveDNSRecord(ctx, "a.test.sind.sind")
	require.NoError(t, err)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

// --- EnsureMesh ---

func TestEnsureMesh(t *testing.T) {
	var m mock.Executor
	// EnsureMeshNetwork: NetworkExists → not found, CreateNetwork → success
	m.AddResult("", "Error: No such network: sind-mesh\n",
		testutil.ExitCode1(t))
	m.AddResult("net-id\n", "", nil)
	// EnsureDNS: ContainerExists → not found, CreateContainer, CopyToContainer, StartContainer
	m.AddResult("", "Error: No such container: sind-dns\n",
		testutil.ExitCode1(t))
	m.AddResult("dns-id\n", "", nil)
	m.AddResult("", "", nil)
	m.AddResult("sind-dns\n", "", nil)
	// EnsureSSHVolume: VolumeExists → not found, CreateVolume, CreateContainer, CopyToContainer, RemoveContainer
	m.AddResult("", "Error: No such volume: sind-ssh-config\n",
		testutil.ExitCode1(t))
	m.AddResult("sind-ssh-config\n", "", nil)
	m.AddResult("keygen-id\n", "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	// EnsureSSH: ContainerExists → not found, InspectDNS, CreateContainer, StartContainer
	m.AddResult("", "Error: No such container: sind-ssh\n",
		testutil.ExitCode1(t))
	m.AddResult(dnsInspectJSON(), "", nil)
	m.AddResult("ssh-id\n", "", nil)
	m.AddResult("sind-ssh\n", "", nil)

	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureMesh(t.Context())
	require.NoError(t, err)
}

func TestEnsureMesh_AllExist(t *testing.T) {
	var m mock.Executor
	// All four exist checks return success.
	m.AddResult("[{}]\n", "", nil) // NetworkExists
	m.AddResult("[{}]\n", "", nil) // ContainerExists (DNS)
	m.AddResult("[{}]\n", "", nil) // VolumeExists (SSH)
	m.AddResult("[{}]\n", "", nil) // ContainerExists (SSH)

	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureMesh(t.Context())
	require.NoError(t, err)
	assert.Len(t, m.Calls, 4)
}

func TestEnsureMesh_NetworkError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureMesh(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mesh network")
}

func TestEnsureMesh_DNSError(t *testing.T) {
	var m mock.Executor
	m.AddResult("[{}]\n", "", nil) // network exists
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureMesh(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "DNS container")
}

func TestEnsureMesh_SSHVolumeError(t *testing.T) {
	var m mock.Executor
	m.AddResult("[{}]\n", "", nil) // network exists
	m.AddResult("[{}]\n", "", nil) // DNS exists
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureMesh(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SSH volume")
}

func TestEnsureMesh_SSHContainerError(t *testing.T) {
	var m mock.Executor
	m.AddResult("[{}]\n", "", nil) // network exists
	m.AddResult("[{}]\n", "", nil) // DNS exists
	m.AddResult("[{}]\n", "", nil) // SSH volume exists
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureMesh(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SSH container")
}

// --- CleanupMesh ---

func TestCleanupMesh(t *testing.T) {
	var m mock.Executor
	// removeContainerIfExists(keygen): not found
	m.AddResult("", "Error\n", testutil.ExitCode1(t))
	// removeContainerIfExists(SSH): exists, rm -f
	m.AddResult("[{}]\n", "", nil)
	m.AddResult("sind-ssh\n", "", nil)
	// removeContainerIfExists(DNS): exists, rm -f
	m.AddResult("[{}]\n", "", nil)
	m.AddResult("sind-dns\n", "", nil)
	// removeNetworkIfExists: exists, remove
	m.AddResult("[{}]\n", "", nil)
	m.AddResult("sind-mesh\n", "", nil)
	// removeVolumeIfExists: exists, remove
	m.AddResult("[{}]\n", "", nil)
	m.AddResult("sind-ssh-config\n", "", nil)

	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.CleanupMesh(t.Context())
	require.NoError(t, err)

	// Verify order: keygen container, SSH container, DNS container, network, volume.
	assert.Equal(t, []string{"container", "inspect", "sind-ssh-keygen"}, m.Calls[0].Args)
	assert.Equal(t, []string{"container", "inspect", string(SSHContainerName)}, m.Calls[1].Args)
	assert.Equal(t, []string{"rm", "-f", string(SSHContainerName)}, m.Calls[2].Args)
	assert.Equal(t, []string{"container", "inspect", string(DNSContainerName)}, m.Calls[3].Args)
	assert.Equal(t, []string{"rm", "-f", string(DNSContainerName)}, m.Calls[4].Args)
	assert.Equal(t, []string{"network", "inspect", string(NetworkName)}, m.Calls[5].Args)
	assert.Equal(t, []string{"network", "rm", string(NetworkName)}, m.Calls[6].Args)
	assert.Equal(t, []string{"volume", "inspect", string(SSHVolumeName)}, m.Calls[7].Args)
	assert.Equal(t, []string{"volume", "rm", string(SSHVolumeName)}, m.Calls[8].Args)
}

func TestCleanupMesh_NoneExist(t *testing.T) {
	var m mock.Executor
	// All five exist checks return not found (keygen, SSH, DNS, network, volume).
	m.AddResult("", "Error\n", testutil.ExitCode1(t))
	m.AddResult("", "Error\n", testutil.ExitCode1(t))
	m.AddResult("", "Error\n", testutil.ExitCode1(t))
	m.AddResult("", "Error\n", testutil.ExitCode1(t))
	m.AddResult("", "Error\n", testutil.ExitCode1(t))

	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.CleanupMesh(t.Context())
	require.NoError(t, err)
	assert.Len(t, m.Calls, 5) // only exist checks, no removes
}

func TestCleanupMesh_SSHContainerError(t *testing.T) {
	var m mock.Executor
	// keygen: not found
	m.AddResult("", "Error\n", testutil.ExitCode1(t))
	// SSH: connection refused
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.CleanupMesh(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "removing SSH container")
}

func TestCleanupMesh_DNSContainerError(t *testing.T) {
	var m mock.Executor
	// keygen: not found
	m.AddResult("", "Error\n", testutil.ExitCode1(t))
	// SSH container doesn't exist
	m.AddResult("", "Error\n", testutil.ExitCode1(t))
	// DNS container check fails
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.CleanupMesh(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "removing DNS container")
}

func TestCleanupMesh_NetworkError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", testutil.ExitCode1(t))     // keygen
	m.AddResult("", "Error\n", testutil.ExitCode1(t))     // SSH
	m.AddResult("", "Error\n", testutil.ExitCode1(t))     // DNS
	m.AddResult("", "", fmt.Errorf("connection refused")) // network
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.CleanupMesh(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "removing mesh network")
}

func TestCleanupMesh_RemoveContainerError(t *testing.T) {
	var m mock.Executor
	// keygen: not found
	m.AddResult("", "Error\n", testutil.ExitCode1(t))
	// SSH container: exists, rm -f fails
	m.AddResult("[{}]\n", "", nil)
	m.AddResult("", "Error: removal in progress\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.CleanupMesh(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "removing SSH container")
}

func TestCleanupMesh_VolumeError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", testutil.ExitCode1(t))     // keygen
	m.AddResult("", "Error\n", testutil.ExitCode1(t))     // SSH
	m.AddResult("", "Error\n", testutil.ExitCode1(t))     // DNS
	m.AddResult("", "Error\n", testutil.ExitCode1(t))     // network
	m.AddResult("", "", fmt.Errorf("connection refused")) // volume
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.CleanupMesh(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "removing SSH volume")
}

// --- EnsureMeshNetwork ---

func TestEnsureMeshNetwork_Creates(t *testing.T) {
	const networkID = "6f02052f0a95e0134b3f284b793c63803306b04225f9dc2b40cf48975a2e743b"

	var m mock.Executor
	// NetworkExists → not found (exit code 1)
	m.AddResult("", "Error: No such network: sind-mesh\n",
		testutil.ExitCode1(t))
	// CreateNetwork → success
	m.AddResult(networkID+"\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	assert.False(t, mgr.Created(), "Created() before EnsureMeshNetwork")
	err := mgr.EnsureMeshNetwork(t.Context())
	require.NoError(t, err)
	assert.True(t, mgr.Created(), "Created() after creating mesh network")

	require.Len(t, m.Calls, 2)
	assert.Equal(t, []string{"network", "inspect", string(NetworkName)}, m.Calls[0].Args)
	assert.Equal(t, []string{
		"network", "create",
		"--label", "com.docker.compose.network=mesh",
		"--label", "com.docker.compose.project=sind-mesh",
		string(NetworkName),
	}, m.Calls[1].Args)
}

func TestEnsureMeshNetwork_AlreadyExists(t *testing.T) {
	var m mock.Executor
	// NetworkExists → found
	m.AddResult("[{}]\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureMeshNetwork(t.Context())
	require.NoError(t, err)
	assert.False(t, mgr.Created(), "Created() when mesh already existed")

	// Only inspect, no create
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "inspect", string(NetworkName)}, m.Calls[0].Args)
}

func TestEnsureMeshNetwork_InspectError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureMeshNetwork(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checking mesh network")
}

func TestEnsureMeshNetwork_CreateError(t *testing.T) {
	var m mock.Executor
	// NetworkExists → not found
	m.AddResult("", "Error: No such network: sind-mesh\n",
		testutil.ExitCode1(t))
	// CreateNetwork → error
	m.AddResult("", "Error: permission denied\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureMeshNetwork(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating mesh network")
}

// --- EnsureDNS ---

func TestEnsureDNS_Creates(t *testing.T) {
	const containerID = "abc123"

	var m mock.Executor
	// ContainerExists → not found
	m.AddResult("", "Error: No such container: sind-dns\n",
		testutil.ExitCode1(t))
	// CreateContainer → success
	m.AddResult(containerID+"\n", "", nil)
	// CopyToContainer → success
	m.AddResult("", "", nil)
	// StartContainer → success
	m.AddResult("sind-dns\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureDNS(t.Context())
	require.NoError(t, err)

	require.Len(t, m.Calls, 4)
	assert.Equal(t, []string{"container", "inspect", string(DNSContainerName)}, m.Calls[0].Args)
	createArgs := m.Calls[1].Args
	assert.Equal(t, "create", createArgs[0])
	assert.Equal(t, "--name", createArgs[1])
	assert.Equal(t, string(DNSContainerName), createArgs[2])
	assert.Equal(t, "--network", createArgs[3])
	assert.Equal(t, string(NetworkName), createArgs[4])
	assert.Contains(t, createArgs, "--label")
	assert.Equal(t, DNSImage, createArgs[len(createArgs)-1])
	assert.Equal(t, []string{"cp", "-", string(DNSContainerName) + ":/"}, m.Calls[2].Args)
	assert.Equal(t, []string{"start", string(DNSContainerName)}, m.Calls[3].Args)

	// Verify initial Corefile has empty hosts block
	corefile := extractTarFile(t, m.Calls[2].Stdin, "Corefile")
	assert.Contains(t, corefile, "hosts {")
	assert.Contains(t, corefile, "fallthrough")
	assert.NotContains(t, corefile, "sind.sind\n")
}

func TestEnsureDNS_AlreadyExists(t *testing.T) {
	var m mock.Executor
	m.AddResult("[{}]\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureDNS(t.Context())
	require.NoError(t, err)
	require.Len(t, m.Calls, 1)
}

func TestEnsureDNS_InspectError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureDNS(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checking DNS container")
}

func TestEnsureDNS_CreateError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error: No such container: sind-dns\n",
		testutil.ExitCode1(t))
	m.AddResult("", "Error: pull access denied\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureDNS(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating DNS container")
}

func TestEnsureDNS_CopyError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error: No such container: sind-dns\n",
		testutil.ExitCode1(t))
	m.AddResult("abc123\n", "", nil)
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureDNS(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing DNS configuration")
}

func TestEnsureDNS_StartError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error: No such container: sind-dns\n",
		testutil.ExitCode1(t))
	m.AddResult("abc123\n", "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "Error: cannot start\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureDNS(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "starting DNS container")
}

func TestEnsureDNS_Pull(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error: No such container: sind-dns\n",
		testutil.ExitCode1(t))
	m.AddResult("abc123\n", "", nil)
	m.AddResult("", "", nil)
	m.AddResult("sind-dns\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)
	mgr.Pull = true

	err := mgr.EnsureDNS(t.Context())
	require.NoError(t, err)

	createArgs := m.Calls[1].Args
	pull, ok := testutil.ArgValue(createArgs, "--pull")
	assert.True(t, ok, "--pull flag present")
	assert.Equal(t, "always", pull)
}

// --- AddDNSRecord ---

func TestAddDNSRecord_Empty(t *testing.T) {
	var m mock.Executor
	// CopyFromContainer → Corefile with empty hosts block
	m.AddResult(corefileTar(t, nil), "", nil)
	// CopyToContainer → success
	m.AddResult("", "", nil)
	// KillContainer → success
	m.AddResult("sind-dns\n", "", nil)
	// StartContainer → success
	m.AddResult("sind-dns\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddDNSRecord(t.Context(), "controller.dev.sind.sind", "172.18.0.2")
	require.NoError(t, err)

	require.Len(t, m.Calls, 4)
	// Verify read
	assert.Equal(t, []string{"cp", string(DNSContainerName) + ":/Corefile", "-"}, m.Calls[0].Args)
	// Verify written Corefile contains the record
	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.Contains(t, corefile, "172.18.0.2 controller.dev.sind.sind")
	// Verify restart
	assert.Equal(t, []string{"kill", string(DNSContainerName)}, m.Calls[2].Args)
	assert.Equal(t, []string{"start", string(DNSContainerName)}, m.Calls[3].Args)
}

func TestAddDNSRecord_Appends(t *testing.T) {
	existing := []string{"172.18.0.2 controller.dev.sind.sind"}

	var m mock.Executor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult("sind-dns\n", "", nil)
	m.AddResult("sind-dns\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddDNSRecord(t.Context(), "worker-0.dev.sind.sind", "172.18.0.3")
	require.NoError(t, err)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.Contains(t, corefile, "172.18.0.2 controller.dev.sind.sind")
	assert.Contains(t, corefile, "172.18.0.3 worker-0.dev.sind.sind")
}

func TestAddDNSRecord_ReadError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddDNSRecord(t.Context(), "controller.dev.sind.sind", "172.18.0.2")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading DNS Corefile")
}

func TestAddDNSRecord_WriteError(t *testing.T) {
	var m mock.Executor
	m.AddResult(corefileTar(t, nil), "", nil)
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddDNSRecord(t.Context(), "controller.dev.sind.sind", "172.18.0.2")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing DNS Corefile")
}

func TestAddDNSRecord_ReloadError(t *testing.T) {
	var m mock.Executor
	m.AddResult(corefileTar(t, nil), "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddDNSRecord(t.Context(), "controller.dev.sind.sind", "172.18.0.2")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reloading DNS")
}

func TestAddDNSRecord_RestartError(t *testing.T) {
	var m mock.Executor
	m.AddResult(corefileTar(t, nil), "", nil)               // read
	m.AddResult("", "", nil)                                // write
	m.AddResult("sind-dns\n", "", nil)                      // kill succeeds
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1")) // start fails
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddDNSRecord(t.Context(), "controller.dev.sind.sind", "172.18.0.2")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reloading DNS")
}

// --- AddDNSRecords (batch) ---

func TestAddDNSRecords(t *testing.T) {
	existing := []string{"172.18.0.2 controller.dev.sind.sind"}

	var m mock.Executor
	m.AddResult(corefileTar(t, existing), "", nil) // read
	m.AddResult("", "", nil)                       // write
	m.AddResult("sind-dns\n", "", nil)             // kill
	m.AddResult("sind-dns\n", "", nil)             // start
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddDNSRecords(t.Context(), []DNSRecord{
		{Hostname: "worker-0.dev.sind.sind", IP: "172.18.0.3"},
		{Hostname: "worker-1.dev.sind.sind", IP: "172.18.0.4"},
	})
	require.NoError(t, err)

	// Only 4 docker calls total (read, write, kill, start).
	require.Len(t, m.Calls, 4)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.Contains(t, corefile, "172.18.0.2 controller.dev.sind.sind")
	assert.Contains(t, corefile, "172.18.0.3 worker-0.dev.sind.sind")
	assert.Contains(t, corefile, "172.18.0.4 worker-1.dev.sind.sind")
}

func TestAddDNSRecords_Dedup(t *testing.T) {
	existing := []string{
		"172.18.0.2 controller.dev.sind.sind",
		"172.18.0.3 worker-0.dev.sind.sind",
	}

	var m mock.Executor
	m.AddResult(corefileTar(t, existing), "", nil) // read
	m.AddResult("", "", nil)                       // write
	m.AddResult("sind-dns\n", "", nil)             // kill
	m.AddResult("sind-dns\n", "", nil)             // start
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	// Re-add controller with same IP — should replace, not duplicate.
	err := mgr.AddDNSRecords(t.Context(), []DNSRecord{
		{Hostname: "controller.dev.sind.sind", IP: "172.18.0.2"},
	})
	require.NoError(t, err)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.Contains(t, corefile, "172.18.0.3 worker-0.dev.sind.sind")
	assert.Contains(t, corefile, "172.18.0.2 controller.dev.sind.sind")
	assert.Equal(t, 1, strings.Count(corefile, "controller.dev.sind.sind"),
		"controller should appear exactly once")
}

func TestAddDNSRecords_ReadError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddDNSRecords(t.Context(), []DNSRecord{
		{Hostname: "controller.dev.sind.sind", IP: "172.18.0.2"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading DNS")
}

// --- RemoveDNSRecords (batch) ---

func TestRemoveDNSRecords(t *testing.T) {
	existing := []string{
		"172.18.0.2 controller.dev.sind.sind",
		"172.18.0.3 worker-0.dev.sind.sind",
		"172.18.0.4 worker-1.dev.sind.sind",
	}

	var m mock.Executor
	m.AddResult(corefileTar(t, existing), "", nil) // read
	m.AddResult("", "", nil)                       // write
	m.AddResult("sind-dns\n", "", nil)             // kill
	m.AddResult("sind-dns\n", "", nil)             // start
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveDNSRecords(t.Context(), []string{
		"controller.dev.sind.sind",
		"worker-1.dev.sind.sind",
	})
	require.NoError(t, err)

	// Only 4 docker calls total (read, write, kill, start).
	require.Len(t, m.Calls, 4)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.NotContains(t, corefile, "controller.dev.sind.sind")
	assert.Contains(t, corefile, "172.18.0.3 worker-0.dev.sind.sind")
	assert.NotContains(t, corefile, "worker-1.dev.sind.sind")
}

func TestRemoveDNSRecords_ReadError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveDNSRecords(t.Context(), []string{"controller.dev.sind.sind"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading DNS")
}

func TestAddDNSRecords_Empty(t *testing.T) {
	var m mock.Executor
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddDNSRecords(t.Context(), nil)
	require.NoError(t, err)
	assert.Empty(t, m.Calls, "no docker calls for empty slice")
}

func TestRemoveDNSRecords_Empty(t *testing.T) {
	var m mock.Executor
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveDNSRecords(t.Context(), nil)
	require.NoError(t, err)
	assert.Empty(t, m.Calls, "no docker calls for empty slice")
}

// --- RemoveDNSRecord ---

func TestRemoveDNSRecord(t *testing.T) {
	existing := []string{
		"172.18.0.2 controller.dev.sind.sind",
		"172.18.0.3 worker-0.dev.sind.sind",
	}

	var m mock.Executor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult("sind-dns\n", "", nil)
	m.AddResult("sind-dns\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveDNSRecord(t.Context(), "controller.dev.sind.sind")
	require.NoError(t, err)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.NotContains(t, corefile, "controller.dev.sind.sind")
	assert.Contains(t, corefile, "172.18.0.3 worker-0.dev.sind.sind")
}

func TestRemoveDNSRecord_LastEntry(t *testing.T) {
	existing := []string{"172.18.0.2 controller.dev.sind.sind"}

	var m mock.Executor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult("sind-dns\n", "", nil)
	m.AddResult("sind-dns\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveDNSRecord(t.Context(), "controller.dev.sind.sind")
	require.NoError(t, err)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.NotContains(t, corefile, "controller.dev.sind.sind")
	// Should still have valid Corefile structure
	assert.Contains(t, corefile, "hosts {")
	assert.Contains(t, corefile, "fallthrough")
}

func TestRemoveDNSRecord_NotFound(t *testing.T) {
	existing := []string{"172.18.0.2 controller.dev.sind.sind"}

	var m mock.Executor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult("sind-dns\n", "", nil)
	m.AddResult("sind-dns\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveDNSRecord(t.Context(), "worker-0.dev.sind.sind")
	require.NoError(t, err)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.Contains(t, corefile, "172.18.0.2 controller.dev.sind.sind")
}

func TestRemoveDNSRecord_ReloadError(t *testing.T) {
	existing := []string{"172.18.0.2 controller.dev.sind.sind"}

	var m mock.Executor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveDNSRecord(t.Context(), "controller.dev.sind.sind")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reloading DNS")
}

func TestRemoveDNSRecord_DuplicateHostnames(t *testing.T) {
	existing := []string{
		"172.18.0.2 controller.dev.sind.sind",
		"172.18.0.5 controller.dev.sind.sind",
		"172.18.0.3 worker-0.dev.sind.sind",
	}

	var m mock.Executor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult("sind-dns\n", "", nil)
	m.AddResult("sind-dns\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveDNSRecord(t.Context(), "controller.dev.sind.sind")
	require.NoError(t, err)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.NotContains(t, corefile, "controller.dev.sind.sind")
	assert.Contains(t, corefile, "172.18.0.3 worker-0.dev.sind.sind")
}

func TestRemoveDNSRecord_ReadError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveDNSRecord(t.Context(), "controller.dev.sind.sind")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading DNS Corefile")
}

func TestRemoveDNSRecord_WriteError(t *testing.T) {
	var m mock.Executor
	m.AddResult(corefileTar(t, []string{"172.18.0.2 x"}), "", nil)
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveDNSRecord(t.Context(), "x")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing DNS Corefile")
}

// --- generateCorefile / parseEntries ---

func TestGenerateCorefile_Empty(t *testing.T) {
	cf := generateCorefile(DefaultRealm, nil)
	assert.Contains(t, cf, "sind.sind:53")
	assert.Contains(t, cf, "hosts {")
	assert.Contains(t, cf, "fallthrough")
	assert.Contains(t, cf, "forward . /etc/resolv.conf")
}

func TestGenerateCorefile_WithEntries(t *testing.T) {
	entries := []string{
		"172.18.0.2 controller.dev.sind.sind",
		"172.18.0.3 worker-0.dev.sind.sind",
	}
	cf := generateCorefile(DefaultRealm, entries)
	assert.Contains(t, cf, "        172.18.0.2 controller.dev.sind.sind\n")
	assert.Contains(t, cf, "        172.18.0.3 worker-0.dev.sind.sind\n")
}

func TestGenerateCorefile_CustomRealm(t *testing.T) {
	cf := generateCorefile("ci42", nil)
	assert.Contains(t, cf, "ci42.sind:53")
}

func TestParseEntries_Empty(t *testing.T) {
	cf := generateCorefile(DefaultRealm, nil)
	entries := parseEntries(cf)
	assert.Empty(t, entries)
}

func TestParseEntries_Roundtrip(t *testing.T) {
	original := []string{
		"172.18.0.2 controller.dev.sind.sind",
		"172.18.0.3 worker-0.dev.sind.sind",
	}
	cf := generateCorefile(DefaultRealm, original)
	parsed := parseEntries(cf)
	assert.Equal(t, original, parsed)
}

func TestParseEntries_NoHostsBlock(t *testing.T) {
	entries := parseEntries(".:53 {\n    forward . /etc/resolv.conf\n}\n")
	assert.Empty(t, entries)
}

// --- Custom Realm ---

func TestCustomRealm_ResourceNames(t *testing.T) {
	c := docker.NewClient(&mock.Executor{})
	mgr := NewManager(c, "testrealm")

	assert.Equal(t, docker.NetworkName("testrealm-mesh"), mgr.NetworkName())
	assert.Equal(t, docker.ContainerName("testrealm-dns"), mgr.DNSContainerName())
	assert.Equal(t, docker.ContainerName("testrealm-ssh"), mgr.SSHContainerName())
	assert.Equal(t, docker.VolumeName("testrealm-ssh-config"), mgr.SSHVolumeName())
	assert.Equal(t, docker.ContainerName("testrealm-ssh-keygen"), mgr.SSHKeygenName())
	assert.Equal(t, "testrealm-mesh", mgr.ComposeProject())
}

func TestCustomRealm_DefaultProducesStandardNames(t *testing.T) {
	c := docker.NewClient(&mock.Executor{})
	mgr := NewManager(c, DefaultRealm)

	assert.Equal(t, NetworkName, mgr.NetworkName())
	assert.Equal(t, DNSContainerName, mgr.DNSContainerName())
	assert.Equal(t, SSHContainerName, mgr.SSHContainerName())
	assert.Equal(t, SSHVolumeName, mgr.SSHVolumeName())
}

func TestCustomRealm_EnsureMeshNetwork(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", testutil.ExitCode1(t)) // NetworkExists → no
	m.AddResult("net-id\n", "", nil)                  // CreateNetwork
	c := docker.NewClient(&m)
	mgr := NewManager(c, "myrealm")

	err := mgr.EnsureMeshNetwork(t.Context())
	require.NoError(t, err)

	require.Len(t, m.Calls, 2)
	assert.Equal(t, []string{"network", "inspect", "myrealm-mesh"}, m.Calls[0].Args)
	assert.Equal(t, []string{
		"network", "create",
		"--label", "com.docker.compose.network=mesh",
		"--label", "com.docker.compose.project=myrealm-mesh",
		"myrealm-mesh",
	}, m.Calls[1].Args)
}

func TestCustomRealm_EnsureDNS(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", testutil.ExitCode1(t)) // ContainerExists → no
	m.AddResult("dns-id\n", "", nil)                  // CreateContainer
	m.AddResult("", "", nil)                          // CopyToContainer
	m.AddResult("myrealm-dns\n", "", nil)             // StartContainer
	c := docker.NewClient(&m)
	mgr := NewManager(c, "myrealm")

	err := mgr.EnsureDNS(t.Context())
	require.NoError(t, err)

	require.Len(t, m.Calls, 4)
	assert.Equal(t, []string{"container", "inspect", "myrealm-dns"}, m.Calls[0].Args)
	createArgs := m.Calls[1].Args
	assert.Equal(t, "--name", createArgs[1])
	assert.Equal(t, "myrealm-dns", createArgs[2])
	assert.Equal(t, "--network", createArgs[3])
	assert.Equal(t, "myrealm-mesh", createArgs[4])
}

func TestCustomRealm_EnsureSSHVolume(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", testutil.ExitCode1(t)) // VolumeExists → no
	m.AddResult("myrealm-ssh-config\n", "", nil)      // CreateVolume
	m.AddResult("keygen-id\n", "", nil)               // CreateContainer
	m.AddResult("", "", nil)                          // CopyToContainer
	m.AddResult("", "", nil)                          // RemoveContainer
	c := docker.NewClient(&m)
	mgr := NewManager(c, "myrealm")

	err := mgr.EnsureSSHVolume(t.Context())
	require.NoError(t, err)

	require.Len(t, m.Calls, 5)
	assert.Equal(t, []string{"volume", "inspect", "myrealm-ssh-config"}, m.Calls[0].Args)
	assert.Equal(t, []string{
		"volume", "create",
		"--label", "com.docker.compose.project=myrealm-mesh",
		"--label", "com.docker.compose.volume=ssh-config",
		"myrealm-ssh-config",
	}, m.Calls[1].Args)
	assert.Equal(t, []string{
		"create",
		"--name", "myrealm-ssh-keygen",
		"-v", "myrealm-ssh-config:/ssh",
		sshKeygenImage,
	}, m.Calls[2].Args)
}

func TestCustomRealm_EnsureSSH(t *testing.T) {
	inspectJSON := `[{"Id":"dns123","Name":"/myrealm-dns","State":{"Status":"running"},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{"myrealm-mesh":{"IPAddress":"10.0.0.2"}}}}]`

	var m mock.Executor
	m.AddResult("", "Error\n", testutil.ExitCode1(t)) // ContainerExists → no
	m.AddResult(inspectJSON, "", nil)                 // InspectContainer(DNS)
	m.AddResult("ssh-id\n", "", nil)                  // CreateContainer
	m.AddResult("myrealm-ssh\n", "", nil)             // StartContainer
	c := docker.NewClient(&m)
	mgr := NewManager(c, "myrealm")

	err := mgr.EnsureSSH(t.Context())
	require.NoError(t, err)

	require.Len(t, m.Calls, 4)
	assert.Equal(t, []string{"container", "inspect", "myrealm-ssh"}, m.Calls[0].Args)
	assert.Equal(t, []string{"inspect", "myrealm-dns"}, m.Calls[1].Args)
	createArgs := m.Calls[2].Args
	assert.Equal(t, "--name", createArgs[1])
	assert.Equal(t, "myrealm-ssh", createArgs[2])
	assert.Equal(t, "--network", createArgs[3])
	assert.Equal(t, "myrealm-mesh", createArgs[4])
	assert.Equal(t, []string{"start", "myrealm-ssh"}, m.Calls[3].Args)
}

func TestCustomRealm_CleanupMesh(t *testing.T) {
	var m mock.Executor
	// keygen container: not found
	m.AddResult("", "Error\n", testutil.ExitCode1(t))
	// SSH container: exists, rm -f
	m.AddResult("[{}]\n", "", nil)
	m.AddResult("myrealm-ssh\n", "", nil)
	// DNS container: exists, rm -f
	m.AddResult("[{}]\n", "", nil)
	m.AddResult("myrealm-dns\n", "", nil)
	// Network: exists, remove
	m.AddResult("[{}]\n", "", nil)
	m.AddResult("myrealm-mesh\n", "", nil)
	// Volume: exists, remove
	m.AddResult("[{}]\n", "", nil)
	m.AddResult("myrealm-ssh-config\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, "myrealm")

	err := mgr.CleanupMesh(t.Context())
	require.NoError(t, err)

	assert.Equal(t, []string{"container", "inspect", "myrealm-ssh-keygen"}, m.Calls[0].Args)
	assert.Equal(t, []string{"container", "inspect", "myrealm-ssh"}, m.Calls[1].Args)
	assert.Equal(t, []string{"rm", "-f", "myrealm-ssh"}, m.Calls[2].Args)
	assert.Equal(t, []string{"container", "inspect", "myrealm-dns"}, m.Calls[3].Args)
	assert.Equal(t, []string{"rm", "-f", "myrealm-dns"}, m.Calls[4].Args)
	assert.Equal(t, []string{"network", "inspect", "myrealm-mesh"}, m.Calls[5].Args)
	assert.Equal(t, []string{"network", "rm", "myrealm-mesh"}, m.Calls[6].Args)
	assert.Equal(t, []string{"volume", "inspect", "myrealm-ssh-config"}, m.Calls[7].Args)
	assert.Equal(t, []string{"volume", "rm", "myrealm-ssh-config"}, m.Calls[8].Args)
}

// dnsInspectJSON returns a mock docker inspect result for the DNS container on the mesh network.
func dnsInspectJSON() string {
	return `[{"Id":"dns123","Name":"/sind-dns","State":{"Status":"running"},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{"sind-mesh":{"IPAddress":"10.0.0.2"}}}}]`
}

// --- GetDNSRecords ---

func TestGetDNSRecords(t *testing.T) {
	var m mock.Executor
	entries := []string{"172.18.0.2 controller.dev.sind.sind", "172.18.0.3 worker-0.dev.sind.sind"}
	m.AddResult(corefileTar(t, entries), "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	records, err := mgr.GetDNSRecords(t.Context())
	require.NoError(t, err)
	require.Len(t, records, 2)
	assert.Equal(t, "172.18.0.2", records[0].IP)
	assert.Equal(t, "controller.dev.sind.sind", records[0].Hostname)
	assert.Equal(t, "172.18.0.3", records[1].IP)
	assert.Equal(t, "worker-0.dev.sind.sind", records[1].Hostname)
}

func TestGetDNSRecords_Empty(t *testing.T) {
	var m mock.Executor
	m.AddResult(corefileTar(t, nil), "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	records, err := mgr.GetDNSRecords(t.Context())
	require.NoError(t, err)
	assert.Empty(t, records)
}

func TestGetDNSRecords_ReadError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", fmt.Errorf("container not found"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	_, err := mgr.GetDNSRecords(t.Context())
	assert.Error(t, err)
}

// --- HostDNS branches ---

// ensureMeshAllExist queues mock results for EnsureMesh where all resources
// already exist, so the function does nothing but check existence.
func ensureMeshAllExist(m *mock.Executor) {
	m.AddResult("[{}]\n", "", nil) // network exists → yes
	m.AddResult("[{}]\n", "", nil) // DNS exists → yes
	m.AddResult("[{}]\n", "", nil) // SSH vol exists → yes
	m.AddResult("[{}]\n", "", nil) // SSH exists → yes
}

// cleanupMeshAllGone queues mock results for CleanupMesh where no resources exist.
func cleanupMeshAllGone(m *mock.Executor, notFound error) {
	m.AddResult("", "Error\n", notFound) // keygen exists → no
	m.AddResult("", "Error\n", notFound) // SSH exists → no
	m.AddResult("", "Error\n", notFound) // DNS exists → no
	m.AddResult("", "Error\n", notFound) // network exists → no
	m.AddResult("", "Error\n", notFound) // SSH vol exists → no
}

func TestEnsureMesh_HostDNS_Skipped(t *testing.T) {
	var dm mock.Executor
	ensureMeshAllExist(&dm)

	var sys mock.Executor
	sys.AddResult("", "", fmt.Errorf("inactive")) // systemctl → not active

	c := docker.NewClient(&dm)
	mgr := NewManager(c, DefaultRealm)
	mgr.HostDNS = true
	mgr.Exec = &sys

	err := mgr.EnsureMesh(t.Context())
	require.NoError(t, err)
}

func TestEnsureMesh_HostDNS_ConfigureFails(t *testing.T) {
	dir := t.TempDir()
	old := sysClassNet
	sysClassNet = dir
	t.Cleanup(func() { sysClassNet = old })
	require.NoError(t, os.Mkdir(filepath.Join(dir, "br-abcdef012345"), 0o755))

	var dm mock.Executor
	ensureMeshAllExist(&dm)
	dm.AddResult(inspectNetworkJSON, "", nil)      // configureHostDNS: InspectNetwork
	dm.AddResult(inspectDNSContainerJSON, "", nil) // configureHostDNS: InspectContainer

	var sys mock.Executor
	sys.AddResult("", "", nil) // systemctl ok
	sys.AddResult("", "", nil) // pkcheck x3
	sys.AddResult("", "", nil)
	sys.AddResult("", "", nil)
	sys.AddResult("", "", fmt.Errorf("denied")) // resolvectl dns fails

	c := docker.NewClient(&dm)
	mgr := NewManager(c, DefaultRealm)
	mgr.HostDNS = true
	mgr.Exec = &sys

	// Error is logged, not returned (best-effort).
	err := mgr.EnsureMesh(t.Context())
	require.NoError(t, err)
}

func TestEnsureMesh_HostDNS_Success(t *testing.T) {
	dir := t.TempDir()
	old := sysClassNet
	sysClassNet = dir
	t.Cleanup(func() { sysClassNet = old })
	require.NoError(t, os.Mkdir(filepath.Join(dir, "br-abcdef012345"), 0o755))

	var dm mock.Executor
	ensureMeshAllExist(&dm)
	dm.AddResult(inspectNetworkJSON, "", nil)      // configureHostDNS: InspectNetwork
	dm.AddResult(inspectDNSContainerJSON, "", nil) // configureHostDNS: InspectContainer

	var sys mock.Executor
	sys.AddResult("", "", nil) // systemctl ok
	sys.AddResult("", "", nil) // pkcheck x3
	sys.AddResult("", "", nil)
	sys.AddResult("", "", nil)
	sys.AddResult("", "", nil) // resolvectl dns
	sys.AddResult("", "", nil) // resolvectl domain

	c := docker.NewClient(&dm)
	mgr := NewManager(c, DefaultRealm)
	mgr.HostDNS = true
	mgr.Exec = &sys

	err := mgr.EnsureMesh(t.Context())
	require.NoError(t, err)
}

func TestCleanupMesh_HostDNS(t *testing.T) {
	var dm mock.Executor
	var sys mock.Executor
	sys.AddResult("", "", fmt.Errorf("inactive")) // systemctl → not active (revertHostDNS skips)

	cleanupMeshAllGone(&dm, testutil.ExitCode1(t))

	c := docker.NewClient(&dm)
	mgr := NewManager(c, DefaultRealm)
	mgr.HostDNS = true
	mgr.Exec = &sys

	err := mgr.CleanupMesh(t.Context())
	require.NoError(t, err)
}

// --- test helpers ---

// corefileTar builds a tar archive containing a Corefile with the given entries,
// matching the format returned by docker cp.
func corefileTar(t *testing.T, entries []string) string {
	t.Helper()
	content := []byte(generateCorefile(DefaultRealm, entries))
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "Corefile", Size: int64(len(content)), Mode: 0644})
	_, _ = tw.Write(content)
	_ = tw.Close()
	return buf.String()
}

// extractTarFile reads a named file from a tar archive captured by MockExecutor.
func extractTarFile(t *testing.T, tarData, name string) string {
	t.Helper()
	tr := tar.NewReader(strings.NewReader(tarData))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			require.Failf(t, "file not found in tar", "%q", name)
		}
		require.NoError(t, err)
		if hdr.Name == name {
			data, err := io.ReadAll(tr)
			require.NoError(t, err)
			return string(data)
		}
	}
}
