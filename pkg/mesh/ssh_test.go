// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- KnownHost Lifecycle ---

func TestKnownHostLifecycle(t *testing.T) {
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

		// AddKnownHost "a"
		rec.AddResult("", "", nil)

		// AddKnownHost "b"
		rec.AddResult("", "", nil)

		// RemoveKnownHost "a": read → write
		rec.AddResult("a.test.sind.sind ssh-ed25519 AAAA\nb.test.sind.sind ssh-ed25519 BBBB\n", "", nil)
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

	// Add two known hosts.
	err = mgr.AddKnownHost(ctx, "a.test.sind.sind", "ssh-ed25519 AAAA")
	require.NoError(t, err)

	err = mgr.AddKnownHost(ctx, "b.test.sind.sind", "ssh-ed25519 BBBB")
	require.NoError(t, err)

	// Remove first host.
	err = mgr.RemoveKnownHost(ctx, "a.test.sind.sind")
	require.NoError(t, err)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

// --- EnsureSSHVolume ---

func TestEnsureSSHVolume_Creates(t *testing.T) {
	const containerID = "abc123"

	var m mock.Executor
	// VolumeExists → not found
	m.AddResult("", "Error: No such volume: sind-ssh-config\n",
		testutil.ExitCode1(t))
	// CreateVolume → success
	m.AddResult(string(SSHVolumeName)+"\n", "", nil)
	// CreateContainer → success
	m.AddResult(containerID+"\n", "", nil)
	// CopyToContainer → success
	m.AddResult("", "", nil)
	// RemoveContainer (defer) → success
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureSSHVolume(t.Context())
	require.NoError(t, err)

	require.Len(t, m.Calls, 5)
	assert.Equal(t, []string{"volume", "inspect", string(SSHVolumeName)}, m.Calls[0].Args)
	assert.Equal(t, []string{
		"volume", "create",
		"--label", "com.docker.compose.project=sind-mesh",
		"--label", "com.docker.compose.volume=ssh-config",
		"--label", "sind.realm=" + DefaultRealm,
		string(SSHVolumeName),
	}, m.Calls[1].Args)
	keygenName := string(mgr.SSHKeygenName())
	assert.Equal(t, []string{
		"create",
		"--name", keygenName,
		"-v", string(SSHVolumeName) + ":/ssh",
		sshKeygenImage,
	}, m.Calls[2].Args)
	assert.Equal(t, []string{"cp", "-", keygenName + ":/ssh"}, m.Calls[3].Args)
	assert.Equal(t, []string{"rm", "-f", keygenName}, m.Calls[4].Args)

	// Verify all three files are in the tar archive.
	privKey := extractTarFile(t, m.Calls[3].Stdin, "id_ed25519")
	pubKey := extractTarFile(t, m.Calls[3].Stdin, "id_ed25519.pub")
	knownHosts := extractTarFile(t, m.Calls[3].Stdin, "known_hosts")

	assert.Contains(t, privKey, "BEGIN OPENSSH PRIVATE KEY")
	assert.Contains(t, privKey, "END OPENSSH PRIVATE KEY")
	assert.True(t, strings.HasPrefix(pubKey, "ssh-ed25519 "))
	assert.Equal(t, "", knownHosts)
}

func TestEnsureSSHVolume_AlreadyExists(t *testing.T) {
	var m mock.Executor
	m.AddResult("[{}]\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureSSHVolume(t.Context())
	require.NoError(t, err)
	require.Len(t, m.Calls, 1)
}

func TestEnsureSSHVolume_CheckError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureSSHVolume(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checking SSH volume")
}

func TestEnsureSSHVolume_CreateVolumeError(t *testing.T) {
	var m mock.Executor
	// VolumeExists → not found
	m.AddResult("", "Error: No such volume: sind-ssh-config\n",
		testutil.ExitCode1(t))
	// CreateVolume → error
	m.AddResult("", "Error: permission denied\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureSSHVolume(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating SSH volume")
}

func TestEnsureSSHVolume_CreateContainerError(t *testing.T) {
	var m mock.Executor
	// VolumeExists → not found
	m.AddResult("", "Error: No such volume: sind-ssh-config\n",
		testutil.ExitCode1(t))
	// CreateVolume → success
	m.AddResult(string(SSHVolumeName)+"\n", "", nil)
	// CreateContainer → error
	m.AddResult("", "Error: pull access denied\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureSSHVolume(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating temporary container")
}

func TestEnsureSSHVolume_CopyError(t *testing.T) {
	var m mock.Executor
	// VolumeExists → not found
	m.AddResult("", "Error: No such volume: sind-ssh-config\n",
		testutil.ExitCode1(t))
	// CreateVolume → success
	m.AddResult(string(SSHVolumeName)+"\n", "", nil)
	// CreateContainer → success
	m.AddResult("abc123\n", "", nil)
	// CopyToContainer → error
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	// RemoveContainer (defer) → success
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureSSHVolume(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing SSH keys")

	// Verify temp container is still cleaned up on error.
	assert.Equal(t, []string{"rm", "-f", string(mgr.SSHKeygenName())}, m.Calls[4].Args)
}

func TestEnsureSSHVolume_Pull(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error: No such volume: sind-ssh-config\n",
		testutil.ExitCode1(t))
	m.AddResult(string(SSHVolumeName)+"\n", "", nil)
	m.AddResult("abc123\n", "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)
	mgr.Pull = true

	err := mgr.EnsureSSHVolume(t.Context())
	require.NoError(t, err)

	createArgs := m.Calls[2].Args
	pull, ok := testutil.ArgValue(createArgs, "--pull")
	assert.True(t, ok, "--pull flag present in keygen container")
	assert.Equal(t, "always", pull)
}

// --- EnsureSSH ---

func TestEnsureSSH_Creates(t *testing.T) {
	const containerID = "def456"

	var m mock.Executor
	// ContainerExists → not found
	m.AddResult("", "Error: No such container: sind-ssh\n",
		testutil.ExitCode1(t))
	// InspectContainer(DNS) → mesh IP
	m.AddResult(dnsInspectJSON(), "", nil)
	// CreateContainer → success
	m.AddResult(containerID+"\n", "", nil)
	// StartContainer → success
	m.AddResult("sind-ssh\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureSSH(t.Context())
	require.NoError(t, err)

	require.Len(t, m.Calls, 4)
	assert.Equal(t, []string{"container", "inspect", string(SSHContainerName)}, m.Calls[0].Args)
	assert.Equal(t, []string{"inspect", string(DNSContainerName)}, m.Calls[1].Args)
	sshCreateArgs := m.Calls[2].Args
	assert.Equal(t, "create", sshCreateArgs[0])
	assert.Equal(t, "--name", sshCreateArgs[1])
	assert.Equal(t, string(SSHContainerName), sshCreateArgs[2])
	assert.Contains(t, sshCreateArgs, "--label")
	assert.Equal(t, "infinity", sshCreateArgs[len(sshCreateArgs)-1])
	assert.Equal(t, "sleep", sshCreateArgs[len(sshCreateArgs)-2])
	assert.Equal(t, SSHImage, sshCreateArgs[len(sshCreateArgs)-3])
	assert.Equal(t, []string{"start", string(SSHContainerName)}, m.Calls[3].Args)
}

func TestEnsureSSH_AlreadyExists(t *testing.T) {
	var m mock.Executor
	m.AddResult("[{}]\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureSSH(t.Context())
	require.NoError(t, err)
	require.Len(t, m.Calls, 1)
}

func TestEnsureSSH_CheckError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureSSH(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checking SSH container")
}

func TestEnsureSSH_InspectDNSError(t *testing.T) {
	var m mock.Executor
	// ContainerExists → not found
	m.AddResult("", "Error: No such container: sind-ssh\n",
		testutil.ExitCode1(t))
	// InspectContainer(DNS) → error
	m.AddResult("", "Error: No such container: sind-dns\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureSSH(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inspecting DNS container")
}

func TestEnsureSSH_CreateError(t *testing.T) {
	var m mock.Executor
	// ContainerExists → not found
	m.AddResult("", "Error: No such container: sind-ssh\n",
		testutil.ExitCode1(t))
	// InspectContainer(DNS) → success
	m.AddResult(dnsInspectJSON(), "", nil)
	// CreateContainer → error
	m.AddResult("", "Error: pull access denied\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureSSH(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating SSH container")
}

func TestEnsureSSH_StartError(t *testing.T) {
	var m mock.Executor
	// ContainerExists → not found
	m.AddResult("", "Error: No such container: sind-ssh\n",
		testutil.ExitCode1(t))
	// InspectContainer(DNS) → success
	m.AddResult(dnsInspectJSON(), "", nil)
	// CreateContainer → success
	m.AddResult("def456\n", "", nil)
	// StartContainer → error
	m.AddResult("", "Error: cannot start\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureSSH(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "starting SSH container")
}

func TestEnsureSSH_Pull(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error: No such container: sind-ssh\n",
		testutil.ExitCode1(t))
	m.AddResult(dnsInspectJSON(), "", nil)
	m.AddResult("def456\n", "", nil)
	m.AddResult("sind-ssh\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)
	mgr.Pull = true

	err := mgr.EnsureSSH(t.Context())
	require.NoError(t, err)

	createArgs := m.Calls[2].Args
	pull, ok := testutil.ArgValue(createArgs, "--pull")
	assert.True(t, ok, "--pull flag present in SSH container")
	assert.Equal(t, "always", pull)
}

// --- AddKnownHost ---

func TestAddKnownHost(t *testing.T) {
	var m mock.Executor
	// AppendFile → success
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddKnownHost(t.Context(),
		"controller.dev.sind.sind", "ssh-ed25519 AAAA...")
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{
		"exec", "-i", string(SSHContainerName),
		"sh", "-c", "cat >> " + knownHostsPath,
	}, m.Calls[0].Args)
	assert.Equal(t, "controller.dev.sind.sind ssh-ed25519 AAAA...\n", m.Calls[0].Stdin)
}

func TestAddKnownHost_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddKnownHost(t.Context(),
		"controller.dev.sind.sind", "ssh-ed25519 AAAA...")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "adding known host controller.dev.sind.sind")
}

// --- AddKnownHosts (batch) ---

func TestAddKnownHosts(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil) // ReadFile (empty known_hosts)
	m.AddResult("", "", nil) // WriteFile
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddKnownHosts(t.Context(), []KnownHostEntry{
		{Hostname: "controller.dev.sind.sind", HostKey: "ssh-ed25519 AAAA..."},
		{Hostname: "worker-0.dev.sind.sind", HostKey: "ssh-ed25519 BBBB..."},
	})
	require.NoError(t, err)

	require.Len(t, m.Calls, 2)
	assert.Equal(t,
		"controller.dev.sind.sind ssh-ed25519 AAAA...\nworker-0.dev.sind.sind ssh-ed25519 BBBB...\n",
		m.Calls[1].Stdin)
}

func TestAddKnownHosts_Dedup(t *testing.T) {
	existing := "controller.dev.sind.sind ssh-ed25519 OLD-KEY\n" +
		"worker-0.dev.sind.sind ssh-ed25519 BBBB...\n"

	var m mock.Executor
	m.AddResult(existing, "", nil) // ReadFile
	m.AddResult("", "", nil)       // WriteFile
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddKnownHosts(t.Context(), []KnownHostEntry{
		{Hostname: "controller.dev.sind.sind", HostKey: "ssh-ed25519 NEW-KEY"},
	})
	require.NoError(t, err)

	// worker-0 preserved, controller replaced with new key.
	assert.Equal(t,
		"worker-0.dev.sind.sind ssh-ed25519 BBBB...\ncontroller.dev.sind.sind ssh-ed25519 NEW-KEY\n",
		m.Calls[1].Stdin)
}

func TestAddKnownHosts_ReadError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("container stopped"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddKnownHosts(t.Context(), []KnownHostEntry{
		{Hostname: "controller.dev.sind.sind", HostKey: "ssh-ed25519 AAAA..."},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading known_hosts")
}

func TestAddKnownHosts_WriteError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)                     // ReadFile
	m.AddResult("", "", fmt.Errorf("disk full")) // WriteFile
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddKnownHosts(t.Context(), []KnownHostEntry{
		{Hostname: "controller.dev.sind.sind", HostKey: "ssh-ed25519 AAAA..."},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "writing known_hosts")
}

func TestAddKnownHosts_Empty(t *testing.T) {
	var m mock.Executor
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddKnownHosts(t.Context(), nil)
	require.NoError(t, err)
	assert.Empty(t, m.Calls, "no docker calls for empty slice")
}

// --- RemoveKnownHosts (batch) ---

func TestRemoveKnownHosts(t *testing.T) {
	existing := "controller.dev.sind.sind ssh-ed25519 AAAA...\n" +
		"worker-0.dev.sind.sind ssh-ed25519 BBBB...\n" +
		"worker-1.dev.sind.sind ssh-ed25519 CCCC...\n"

	var m mock.Executor
	m.AddResult(existing, "", nil) // ReadFile
	m.AddResult("", "", nil)       // WriteFile
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveKnownHosts(t.Context(), []string{
		"controller.dev.sind.sind",
		"worker-1.dev.sind.sind",
	})
	require.NoError(t, err)

	require.Len(t, m.Calls, 2)
	assert.Equal(t, "worker-0.dev.sind.sind ssh-ed25519 BBBB...\n", m.Calls[1].Stdin)
}

func TestRemoveKnownHosts_Empty(t *testing.T) {
	var m mock.Executor
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveKnownHosts(t.Context(), nil)
	require.NoError(t, err)
	assert.Empty(t, m.Calls, "no docker calls for empty slice")
}

func TestRemoveKnownHosts_ReadError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("container stopped"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveKnownHosts(t.Context(), []string{"controller.dev.sind.sind"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading known_hosts")
}

func TestRemoveKnownHosts_WriteError(t *testing.T) {
	var m mock.Executor
	m.AddResult("controller.dev.sind.sind ssh-ed25519 AAAA...\n", "", nil) // ReadFile
	m.AddResult("", "", fmt.Errorf("disk full"))                           // WriteFile
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveKnownHosts(t.Context(), []string{"controller.dev.sind.sind"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "writing known_hosts")
}

// --- RemoveKnownHost ---

func TestRemoveKnownHost(t *testing.T) {
	existing := "controller.dev.sind.sind ssh-ed25519 AAAA...\n" +
		"worker-0.dev.sind.sind ssh-ed25519 BBBB...\n"

	var m mock.Executor
	// ReadFile → existing content
	m.AddResult(existing, "", nil)
	// WriteFile → success
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveKnownHost(t.Context(), "controller.dev.sind.sind")
	require.NoError(t, err)

	require.Len(t, m.Calls, 2)
	assert.Equal(t, "worker-0.dev.sind.sind ssh-ed25519 BBBB...\n", m.Calls[1].Stdin)
}

func TestRemoveKnownHost_LastEntry(t *testing.T) {
	existing := "controller.dev.sind.sind ssh-ed25519 AAAA...\n"

	var m mock.Executor
	m.AddResult(existing, "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveKnownHost(t.Context(), "controller.dev.sind.sind")
	require.NoError(t, err)

	// Should write empty content.
	assert.Equal(t, "", m.Calls[1].Stdin)
}

func TestRemoveKnownHost_DuplicateHostnames(t *testing.T) {
	existing := "controller.dev.sind.sind ssh-ed25519 AAAA...\n" +
		"controller.dev.sind.sind ssh-ed25519 BBBB...\n" +
		"worker-0.dev.sind.sind ssh-ed25519 CCCC...\n"

	var m mock.Executor
	m.AddResult(existing, "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveKnownHost(t.Context(), "controller.dev.sind.sind")
	require.NoError(t, err)

	assert.Equal(t, "worker-0.dev.sind.sind ssh-ed25519 CCCC...\n", m.Calls[1].Stdin)
}

func TestRemoveKnownHost_NotFound(t *testing.T) {
	existing := "controller.dev.sind.sind ssh-ed25519 AAAA...\n"

	var m mock.Executor
	m.AddResult(existing, "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveKnownHost(t.Context(), "worker-0.dev.sind.sind")
	require.NoError(t, err)

	// Should preserve existing content.
	assert.Equal(t, "controller.dev.sind.sind ssh-ed25519 AAAA...\n", m.Calls[1].Stdin)
}

func TestRemoveKnownHost_ReadError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveKnownHost(t.Context(), "controller.dev.sind.sind")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading known_hosts")
}

func TestRemoveKnownHost_WriteError(t *testing.T) {
	var m mock.Executor
	m.AddResult("controller.dev.sind.sind ssh-ed25519 AAAA...\n", "", nil)
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveKnownHost(t.Context(), "controller.dev.sind.sind")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing known_hosts")
}

// --- generateKeypair ---

func TestGenerateKeypair(t *testing.T) {
	privPEM, pubLine := generateKeypair()

	// Verify private key PEM structure.
	block, rest := pem.Decode(privPEM)
	require.NotNil(t, block, "failed to decode PEM")
	assert.Equal(t, "OPENSSH PRIVATE KEY", block.Type)
	assert.Empty(t, rest)

	// Verify AUTH_MAGIC.
	assert.True(t, strings.HasPrefix(string(block.Bytes), "openssh-key-v1\x00"))

	// Verify public key format.
	line := strings.TrimSpace(string(pubLine))
	parts := strings.SplitN(line, " ", 3)
	require.Len(t, parts, 2)
	assert.Equal(t, "ssh-ed25519", parts[0])

	// Decode public key blob and verify structure.
	blob, err := base64.StdEncoding.DecodeString(parts[1])
	require.NoError(t, err)

	// blob = string("ssh-ed25519") + string(pubkey_32_bytes)
	keyType, blob := parseSSHString(t, blob)
	assert.Equal(t, "ssh-ed25519", string(keyType))

	pubKeyBytes, _ := parseSSHString(t, blob)
	assert.Len(t, pubKeyBytes, ed25519.PublicKeySize)
}

func TestGenerateKeypair_KeysMatch(t *testing.T) {
	privPEM, pubLine := generateKeypair()

	// Extract public key from the public key line.
	line := strings.TrimSpace(string(pubLine))
	parts := strings.SplitN(line, " ", 3)
	blob, err := base64.StdEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	_, blob = parseSSHString(t, blob) // skip keytype
	pubFromLine, _ := parseSSHString(t, blob)

	// Extract public key from the private key PEM.
	block, _ := pem.Decode(privPEM)
	data := block.Bytes[len("openssh-key-v1\x00"):]

	// Skip cipher, kdf, kdfoptions.
	_, data = parseSSHString(t, data)
	_, data = parseSSHString(t, data)
	_, data = parseSSHString(t, data)

	// Skip number of keys.
	data = data[4:]

	// Parse public key section.
	pubSection, _ := parseSSHString(t, data)
	_, pubSection = parseSSHString(t, pubSection) // skip keytype
	pubFromPriv, _ := parseSSHString(t, pubSection)

	assert.Equal(t, pubFromLine, pubFromPriv, "public keys from private and public files should match")
}

// --- GetSSH* ---

func TestGetSSHPrivateKey(t *testing.T) {
	var m mock.Executor
	m.AddResult("-----BEGIN OPENSSH PRIVATE KEY-----\nfake\n-----END OPENSSH PRIVATE KEY-----\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	key, err := mgr.GetSSHPrivateKey(t.Context())
	require.NoError(t, err)
	assert.Contains(t, key, "BEGIN OPENSSH PRIVATE KEY")
	assert.Equal(t, []string{"exec", "sind-ssh", "cat", "/root/.ssh/id_ed25519"}, m.Calls[0].Args)
}

func TestGetSSHPrivateKey_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	_, err := mgr.GetSSHPrivateKey(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading SSH private key")
}

func TestGetSSHPublicKey(t *testing.T) {
	var m mock.Executor
	m.AddResult("ssh-ed25519 AAAA... comment\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	key, err := mgr.GetSSHPublicKey(t.Context())
	require.NoError(t, err)
	assert.Contains(t, key, "ssh-ed25519")
	assert.Equal(t, []string{"exec", "sind-ssh", "cat", "/root/.ssh/id_ed25519.pub"}, m.Calls[0].Args)
}

func TestGetSSHKnownHosts(t *testing.T) {
	var m mock.Executor
	m.AddResult("host1 ssh-ed25519 AAAA...\nhost2 ssh-ed25519 BBBB...\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	hosts, err := mgr.GetSSHKnownHosts(t.Context())
	require.NoError(t, err)
	assert.Contains(t, hosts, "host1")
	assert.Contains(t, hosts, "host2")
	assert.Equal(t, []string{"exec", "sind-ssh", "cat", "/root/.ssh/known_hosts"}, m.Calls[0].Args)
}

func TestGetSSHPublicKey_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	_, err := mgr.GetSSHPublicKey(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading SSH public key")
}

func TestGetSSHKnownHosts_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	_, err := mgr.GetSSHKnownHosts(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading SSH known_hosts")
}

// --- test helpers ---

// parseSSHString reads an SSH wire string (uint32 length + data) from buf.
func parseSSHString(t *testing.T, buf []byte) (data, rest []byte) {
	t.Helper()
	require.True(t, len(buf) >= 4, "buffer too short for SSH string length")
	n := binary.BigEndian.Uint32(buf[:4])
	require.True(t, len(buf) >= int(4+n), "buffer too short for SSH string data")
	return buf[4 : 4+n], buf[4+n:]
}
