// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"archive/tar"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/config"
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
		rec.SetOnCall(newFake(t).onCall)
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

	knownHosts, err := mgr.GetSSHKnownHosts(ctx)
	require.NoError(t, err)
	assert.Equal(t, "b.test.sind.sind ssh-ed25519 BBBB\n", knownHosts)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

// --- ensureSSH ---

// TestEnsureSSH_Keys checks the keys a new relay gets: an ed25519 keypair
// and an empty known_hosts, copied into the SSH volume through the relay
// before it starts.
func TestEnsureSSH_Keys(t *testing.T) {
	_, c, m := newFakeDocker(t)
	mgr := NewManager(c, DefaultRealm)

	require.NoError(t, mgr.EnsureMesh(t.Context()))

	i := indexOf(t, m, "cp - sind-ssh:/root/.ssh")
	assert.Less(t, i, indexOf(t, m, "start sind-ssh"), "keys before start")
	stdin := m.Calls[i].Stdin
	privKey := extractTarFile(t, stdin, "id_ed25519")
	pubKey := extractTarFile(t, stdin, "id_ed25519.pub")
	knownHosts := extractTarFile(t, stdin, "known_hosts")
	assert.Contains(t, privKey, "BEGIN OPENSSH PRIVATE KEY")
	assert.Contains(t, privKey, "END OPENSSH PRIVATE KEY")
	assert.True(t, strings.HasPrefix(pubKey, "ssh-ed25519 "))
	assert.Equal(t, "", knownHosts)
	assert.Equal(t, map[string]int64{"id_ed25519": 0o600, "id_ed25519.pub": 0o644, "known_hosts": 0o644}, tarModes(t, stdin))
}

// TestEnsureSSH_KeysIntoExistingRelay covers a new volume under an existing
// relay: the keys go in through the relay.
func TestEnsureSSH_KeysIntoExistingRelay(t *testing.T) {
	f, c, _ := newFakeDocker(t)
	f.withMesh(DefaultRealm, docker.StateRunning)
	mgr := NewManager(c, DefaultRealm)

	require.NoError(t, mgr.ensureSSH(t.Context(), "10.0.0.2", true))

	assert.Contains(t, f.containers["sind-ssh"].files, "/root/.ssh/id_ed25519")
}

func TestEnsureSSH_Creates(t *testing.T) {
	_, c, m := newFakeDocker(t)
	mgr := NewManager(c, DefaultRealm)

	require.NoError(t, mgr.ensureSSH(t.Context(), "10.0.0.2", false))

	create := m.Calls[indexOf(t, m, "create")].Args
	assert.Equal(t, []string{"create", "--name", string(SSHContainerName), "--network", string(NetworkName), "--dns", "10.0.0.2",
		"-v", string(SSHVolumeName) + ":/root/.ssh"}, create[:9])
	assert.Equal(t, []string{SSHImage(), "sleep", "infinity"}, create[len(create)-3:])
	assert.Empty(t, calls(m, "cp"), "no keys without writeKeys")
	assert.Equal(t, []string{"start sind-ssh"}, calls(m, "start"))
}

func TestSSHImage_DefaultNodeImage(t *testing.T) {
	assert.Equal(t, config.DefaultImage, SSHImage())
}

// tarModes returns the mode of each file in a tar archive.
func tarModes(t *testing.T, data string) map[string]int64 {
	t.Helper()
	modes := map[string]int64{}
	tr := tar.NewReader(strings.NewReader(data))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return modes
		}
		require.NoError(t, err)
		modes[hdr.Name] = hdr.Mode
	}
}

// --- AddKnownHost ---

func TestAddKnownHost(t *testing.T) {
	// An existing entry for the host is replaced, not duplicated.
	var m mock.Executor
	m.AddResult("controller.dev.sind.sind ssh-ed25519 OLD\nworker-0.dev.sind.sind ssh-ed25519 W\n", "", nil) // ReadFile
	m.AddResult("", "", nil)                                                                                 // WriteFile
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddKnownHost(t.Context(),
		"controller.dev.sind.sind", "ssh-ed25519 AAAA...")
	require.NoError(t, err)

	require.Len(t, m.Calls, 2)
	assert.Equal(t, []string{
		"exec", "-i", string(SSHContainerName),
		"sh", "-c", "cat > " + knownHostsPath,
	}, m.Calls[1].Args)
	assert.Equal(t, "worker-0.dev.sind.sind ssh-ed25519 W\ncontroller.dev.sind.sind ssh-ed25519 AAAA...\n", m.Calls[1].Stdin)
}

func TestAddKnownHost_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddKnownHost(t.Context(),
		"controller.dev.sind.sind", "ssh-ed25519 AAAA...")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading known_hosts")
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
	m.AddResult("[{}]\n", "", nil) // SSH container exists
	m.AddResult("-----BEGIN OPENSSH PRIVATE KEY-----\nfake\n-----END OPENSSH PRIVATE KEY-----\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	key, err := mgr.GetSSHPrivateKey(t.Context())
	require.NoError(t, err)
	assert.Contains(t, key, "BEGIN OPENSSH PRIVATE KEY")
	assert.Equal(t, []string{"exec", "sind-ssh", "cat", "/root/.ssh/id_ed25519"}, m.Calls[1].Args)
}

func TestGetSSHPrivateKey_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("[{}]\n", "", nil) // SSH container exists
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	_, err := mgr.GetSSHPrivateKey(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading SSH private key")
}

func TestGetSSHPublicKey(t *testing.T) {
	var m mock.Executor
	m.AddResult("[{}]\n", "", nil) // SSH container exists
	m.AddResult("ssh-ed25519 AAAA... comment\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	key, err := mgr.GetSSHPublicKey(t.Context())
	require.NoError(t, err)
	assert.Contains(t, key, "ssh-ed25519")
	assert.Equal(t, []string{"exec", "sind-ssh", "cat", "/root/.ssh/id_ed25519.pub"}, m.Calls[1].Args)
}

func TestGetSSHKnownHosts(t *testing.T) {
	var m mock.Executor
	m.AddResult("[{}]\n", "", nil) // SSH container exists
	m.AddResult("host1 ssh-ed25519 AAAA...\nhost2 ssh-ed25519 BBBB...\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	hosts, err := mgr.GetSSHKnownHosts(t.Context())
	require.NoError(t, err)
	assert.Contains(t, hosts, "host1")
	assert.Contains(t, hosts, "host2")
	assert.Equal(t, []string{"exec", "sind-ssh", "cat", "/root/.ssh/known_hosts"}, m.Calls[1].Args)
}

func TestGetSSHPublicKey_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("[{}]\n", "", nil) // SSH container exists
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	_, err := mgr.GetSSHPublicKey(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading SSH public key")
}

func TestGetSSHKnownHosts_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("[{}]\n", "", nil) // SSH container exists
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	_, err := mgr.GetSSHKnownHosts(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading SSH known_hosts")
}

// TestGetSSH*_NoMesh cover the typo/empty-realm case: the SSH container is
// absent, so the getter must surface a clean "no mesh found" error instead
// of leaking the raw docker exec failure.
func TestGetSSHPrivateKey_NoMesh(t *testing.T) {
	var m mock.Executor
	m.AddResult("", testutil.NoSuchContainer("sind-ssh"), &exec.ExitError{ProcessState: exitCode1(t)})
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	_, err := mgr.GetSSHPrivateKey(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), `no mesh found for realm "sind"`)
	assert.NotContains(t, err.Error(), "exit status")
}

func TestGetSSHPublicKey_NoMesh(t *testing.T) {
	var m mock.Executor
	m.AddResult("", testutil.NoSuchContainer("sind-ssh"), &exec.ExitError{ProcessState: exitCode1(t)})
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	_, err := mgr.GetSSHPublicKey(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), `no mesh found for realm "sind"`)
}

func TestGetSSHKnownHosts_NoMesh(t *testing.T) {
	var m mock.Executor
	m.AddResult("", testutil.NoSuchContainer("sind-ssh"), &exec.ExitError{ProcessState: exitCode1(t)})
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	_, err := mgr.GetSSHKnownHosts(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), `no mesh found for realm "sind"`)
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
