// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- EnsureSSHVolume ---

func TestEnsureSSHVolume_Creates(t *testing.T) {
	const containerID = "abc123"

	var m docker.MockExecutor
	// VolumeExists → not found
	m.AddResult("", "Error: No such volume: sind-ssh-config\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	// CreateVolume → success
	m.AddResult(string(cluster.SSHVolumeName)+"\n", "", nil)
	// CreateContainer → success
	m.AddResult(containerID+"\n", "", nil)
	// CopyToContainer → success
	m.AddResult("", "", nil)
	// RemoveContainer (defer) → success
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureSSHVolume(context.Background())
	require.NoError(t, err)

	require.Len(t, m.Calls, 5)
	assert.Equal(t, []string{"volume", "inspect", string(cluster.SSHVolumeName)}, m.Calls[0].Args)
	assert.Equal(t, []string{"volume", "create", string(cluster.SSHVolumeName)}, m.Calls[1].Args)
	assert.Equal(t, []string{
		"create",
		"--name", string(sshKeygenContainerName),
		"-v", string(cluster.SSHVolumeName) + ":/ssh",
		sshKeygenImage,
	}, m.Calls[2].Args)
	assert.Equal(t, []string{"cp", "-", string(sshKeygenContainerName) + ":/ssh"}, m.Calls[3].Args)
	assert.Equal(t, []string{"rm", string(sshKeygenContainerName)}, m.Calls[4].Args)

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
	var m docker.MockExecutor
	m.AddResult("[{}]\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureSSHVolume(context.Background())
	require.NoError(t, err)
	require.Len(t, m.Calls, 1)
}

func TestEnsureSSHVolume_CheckError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureSSHVolume(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checking SSH volume")
}

func TestEnsureSSHVolume_CreateVolumeError(t *testing.T) {
	var m docker.MockExecutor
	// VolumeExists → not found
	m.AddResult("", "Error: No such volume: sind-ssh-config\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	// CreateVolume → error
	m.AddResult("", "Error: permission denied\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureSSHVolume(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating SSH volume")
}

func TestEnsureSSHVolume_CreateContainerError(t *testing.T) {
	var m docker.MockExecutor
	// VolumeExists → not found
	m.AddResult("", "Error: No such volume: sind-ssh-config\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	// CreateVolume → success
	m.AddResult(string(cluster.SSHVolumeName)+"\n", "", nil)
	// CreateContainer → error
	m.AddResult("", "Error: pull access denied\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureSSHVolume(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating temporary container")
}

func TestEnsureSSHVolume_CopyError(t *testing.T) {
	var m docker.MockExecutor
	// VolumeExists → not found
	m.AddResult("", "Error: No such volume: sind-ssh-config\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	// CreateVolume → success
	m.AddResult(string(cluster.SSHVolumeName)+"\n", "", nil)
	// CreateContainer → success
	m.AddResult("abc123\n", "", nil)
	// CopyToContainer → error
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	// RemoveContainer (defer) → success
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureSSHVolume(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing SSH keys")

	// Verify temp container is still cleaned up on error.
	assert.Equal(t, []string{"rm", string(sshKeygenContainerName)}, m.Calls[4].Args)
}

// --- EnsureSSH ---

func TestEnsureSSH_Creates(t *testing.T) {
	const containerID = "def456"

	var m docker.MockExecutor
	// ContainerExists → not found
	m.AddResult("", "Error: No such container: sind-ssh\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	// CreateContainer → success
	m.AddResult(containerID+"\n", "", nil)
	// StartContainer → success
	m.AddResult("sind-ssh\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureSSH(context.Background())
	require.NoError(t, err)

	require.Len(t, m.Calls, 3)
	assert.Equal(t, []string{"container", "inspect", string(cluster.SSHContainerName)}, m.Calls[0].Args)
	assert.Equal(t, []string{
		"create",
		"--name", string(cluster.SSHContainerName),
		"--network", string(cluster.MeshNetworkName),
		"-v", string(cluster.SSHVolumeName) + ":/root/.ssh",
		SSHImage,
		"sleep", "infinity",
	}, m.Calls[1].Args)
	assert.Equal(t, []string{"start", string(cluster.SSHContainerName)}, m.Calls[2].Args)
}

func TestEnsureSSH_AlreadyExists(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("[{}]\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureSSH(context.Background())
	require.NoError(t, err)
	require.Len(t, m.Calls, 1)
}

func TestEnsureSSH_CheckError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureSSH(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checking SSH container")
}

func TestEnsureSSH_CreateError(t *testing.T) {
	var m docker.MockExecutor
	// ContainerExists → not found
	m.AddResult("", "Error: No such container: sind-ssh\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	// CreateContainer → error
	m.AddResult("", "Error: pull access denied\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureSSH(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating SSH container")
}

func TestEnsureSSH_StartError(t *testing.T) {
	var m docker.MockExecutor
	// ContainerExists → not found
	m.AddResult("", "Error: No such container: sind-ssh\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	// CreateContainer → success
	m.AddResult("def456\n", "", nil)
	// StartContainer → error
	m.AddResult("", "Error: cannot start\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureSSH(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "starting SSH container")
}

// --- AddKnownHost ---

func TestAddKnownHost(t *testing.T) {
	var m docker.MockExecutor
	// AppendFile → success
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.AddKnownHost(context.Background(),
		"controller.dev.sind.local", "ssh-ed25519 AAAA...")
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{
		"exec", "-i", string(cluster.SSHContainerName),
		"sh", "-c", "cat >> " + knownHostsPath,
	}, m.Calls[0].Args)
	assert.Equal(t, "controller.dev.sind.local ssh-ed25519 AAAA...\n", m.Calls[0].Stdin)
}

func TestAddKnownHost_Error(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.AddKnownHost(context.Background(),
		"controller.dev.sind.local", "ssh-ed25519 AAAA...")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "adding known host controller.dev.sind.local")
}

// --- RemoveKnownHost ---

func TestRemoveKnownHost(t *testing.T) {
	existing := "controller.dev.sind.local ssh-ed25519 AAAA...\n" +
		"compute-0.dev.sind.local ssh-ed25519 BBBB...\n"

	var m docker.MockExecutor
	// ReadFile → existing content
	m.AddResult(existing, "", nil)
	// WriteFile → success
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.RemoveKnownHost(context.Background(), "controller.dev.sind.local")
	require.NoError(t, err)

	require.Len(t, m.Calls, 2)
	assert.Equal(t, "compute-0.dev.sind.local ssh-ed25519 BBBB...\n", m.Calls[1].Stdin)
}

func TestRemoveKnownHost_LastEntry(t *testing.T) {
	existing := "controller.dev.sind.local ssh-ed25519 AAAA...\n"

	var m docker.MockExecutor
	m.AddResult(existing, "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.RemoveKnownHost(context.Background(), "controller.dev.sind.local")
	require.NoError(t, err)

	// Should write empty content.
	assert.Equal(t, "", m.Calls[1].Stdin)
}

func TestRemoveKnownHost_NotFound(t *testing.T) {
	existing := "controller.dev.sind.local ssh-ed25519 AAAA...\n"

	var m docker.MockExecutor
	m.AddResult(existing, "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.RemoveKnownHost(context.Background(), "compute-0.dev.sind.local")
	require.NoError(t, err)

	// Should preserve existing content.
	assert.Equal(t, "controller.dev.sind.local ssh-ed25519 AAAA...\n", m.Calls[1].Stdin)
}

func TestRemoveKnownHost_ReadError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.RemoveKnownHost(context.Background(), "controller.dev.sind.local")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading known_hosts")
}

func TestRemoveKnownHost_WriteError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("controller.dev.sind.local ssh-ed25519 AAAA...\n", "", nil)
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.RemoveKnownHost(context.Background(), "controller.dev.sind.local")
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

// --- test helpers ---

// parseSSHString reads an SSH wire string (uint32 length + data) from buf.
func parseSSHString(t *testing.T, buf []byte) (data, rest []byte) {
	t.Helper()
	require.True(t, len(buf) >= 4, "buffer too short for SSH string length")
	n := binary.BigEndian.Uint32(buf[:4])
	require.True(t, len(buf) >= int(4+n), "buffer too short for SSH string data")
	return buf[4 : 4+n], buf[4+n:]
}
