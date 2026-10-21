// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
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
	err = mgr.AddKnownHosts(ctx, []KnownHostEntry{{"a.test.sind.sind", "ssh-ed25519 AAAA"}})
	require.NoError(t, err)

	err = mgr.AddKnownHosts(ctx, []KnownHostEntry{{"b.test.sind.sind", "ssh-ed25519 BBBB"}})
	require.NoError(t, err)

	// Remove first host.
	err = mgr.RemoveKnownHosts(ctx, []string{"a.test.sind.sind"})
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
	assert.Equal(t, map[string]int64{"id_ed25519": 0o600, "id_ed25519.pub": 0o644, "known_hosts": 0o644}, testutil.TarModes(t, stdin))
}

// TestEnsureSSH_KeysIntoExistingRelay covers a new volume under an existing
// relay: the keys go in through the relay.
func TestEnsureSSH_KeysIntoExistingRelay(t *testing.T) {
	f, c, _ := newFakeDocker(t)
	f.withMesh(DefaultRealm, docker.StateRunning)
	mgr := NewManager(c, DefaultRealm)

	changed, err := mgr.ensureSSH(t.Context(), "10.0.0.2", true)

	require.NoError(t, err)
	assert.True(t, changed, "the keys changed the relay")
	assert.Contains(t, f.containers["sind-ssh"].files, "/root/.ssh/id_ed25519")
}

func TestEnsureSSH_Creates(t *testing.T) {
	_, c, m := newFakeDocker(t)
	mgr := NewManager(c, DefaultRealm)

	changed, err := mgr.ensureSSH(t.Context(), "10.0.0.2", false)

	require.NoError(t, err)
	assert.True(t, changed)
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

// --- AddKnownHosts / RemoveKnownHosts ---

func addHosts(entries ...KnownHostEntry) func(*Manager) error {
	return func(m *Manager) error { return m.AddKnownHosts(context.Background(), entries) }
}

func removeHosts(hostnames ...string) func(*Manager) error {
	return func(m *Manager) error { return m.RemoveKnownHosts(context.Background(), hostnames) }
}

func TestEditKnownHosts(t *testing.T) {
	const ctl, w0, w1 = "controller.dev.sind.sind", "worker-0.dev.sind.sind", "worker-1.dev.sind.sind"
	tests := []struct {
		name, existing string
		edit           func(*Manager) error
		want           string
	}{
		{"add", "", addHosts(KnownHostEntry{ctl, "ssh-ed25519 A"}, KnownHostEntry{w0, "ssh-ed25519 B"}),
			ctl + " ssh-ed25519 A\n" + w0 + " ssh-ed25519 B\n"},
		{"add replaces", ctl + " ssh-ed25519 OLD\n" + w0 + " ssh-ed25519 B\n", addHosts(KnownHostEntry{ctl, "ssh-ed25519 NEW"}),
			w0 + " ssh-ed25519 B\n" + ctl + " ssh-ed25519 NEW\n"},
		{"remove", ctl + " ssh-ed25519 A\n" + w0 + " ssh-ed25519 B\n" + w1 + " ssh-ed25519 C\n", removeHosts(ctl, w1),
			w0 + " ssh-ed25519 B\n"},
		{"remove last", ctl + " ssh-ed25519 A\n", removeHosts(ctl), ""},
		{"remove duplicates", ctl + " ssh-ed25519 A\n" + ctl + " ssh-ed25519 B\n" + w0 + " ssh-ed25519 C\n", removeHosts(ctl),
			w0 + " ssh-ed25519 C\n"},
		{"remove missing", ctl + " ssh-ed25519 A\n", removeHosts(w0), ctl + " ssh-ed25519 A\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			m.AddResult(tt.existing, "", nil) // ReadFile
			m.AddResult("", "", nil)          // WriteFile
			require.NoError(t, tt.edit(NewManager(docker.NewClient(&m), DefaultRealm)))

			require.Len(t, m.Calls, 2)
			assert.Equal(t, []string{"exec", "-i", string(SSHContainerName), "sh", "-c", "cat > " + knownHostsPath}, m.Calls[1].Args)
			assert.Equal(t, tt.want, m.Calls[1].Stdin)
		})
	}
}

func TestEditKnownHosts_Errors(t *testing.T) {
	for _, edit := range []func(*Manager) error{addHosts(KnownHostEntry{"h", "k"}), removeHosts("h")} {
		var m mock.Executor
		m.AddResult("", "", fmt.Errorf("container stopped"))
		assert.ErrorContains(t, edit(NewManager(docker.NewClient(&m), DefaultRealm)), "reading known_hosts")

		m = mock.Executor{}
		m.AddResult("h k\n", "", nil)
		m.AddResult("", "", fmt.Errorf("disk full"))
		assert.ErrorContains(t, edit(NewManager(docker.NewClient(&m), DefaultRealm)), "writing known_hosts")
	}
	for _, edit := range []func(*Manager) error{addHosts(), removeHosts()} {
		var m mock.Executor
		require.NoError(t, edit(NewManager(docker.NewClient(&m), DefaultRealm)))
		assert.Empty(t, m.Calls, "no docker calls for nothing to edit")
	}
}

// --- generateKeypair ---

func TestGenerateKeypair(t *testing.T) {
	privPEM, pubLine := generateKeypair()

	signer, err := ssh.ParsePrivateKey(privPEM)
	require.NoError(t, err)
	pub, _, _, rest, err := ssh.ParseAuthorizedKey(pubLine)
	require.NoError(t, err)
	assert.Empty(t, rest)
	assert.Equal(t, ssh.KeyAlgoED25519, pub.Type())
	assert.Equal(t, pub.Marshal(), signer.PublicKey().Marshal(), "the files hold one keypair")
}

// --- GetSSH* ---

func TestGetSSHFiles(t *testing.T) {
	for _, tt := range []struct {
		path, what string
		get        func(*Manager, context.Context) (string, error)
	}{
		{"/root/.ssh/id_ed25519", "SSH private key", (*Manager).GetSSHPrivateKey},
		{"/root/.ssh/id_ed25519.pub", "SSH public key", (*Manager).GetSSHPublicKey},
		{"/root/.ssh/known_hosts", "SSH known_hosts", (*Manager).GetSSHKnownHosts},
	} {
		t.Run(tt.what, func(t *testing.T) {
			var m mock.Executor
			m.AddResult("[{}]\n", "", nil) // SSH container exists
			m.AddResult("content\n", "", nil)
			got, err := tt.get(NewManager(docker.NewClient(&m), DefaultRealm), t.Context())
			require.NoError(t, err)
			assert.Equal(t, "content\n", got)
			assert.Equal(t, []string{"exec", "sind-ssh", "cat", tt.path}, m.Calls[1].Args)

			m = mock.Executor{}
			m.AddResult("[{}]\n", "", nil)
			m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
			_, err = tt.get(NewManager(docker.NewClient(&m), DefaultRealm), t.Context())
			assert.ErrorContains(t, err, "reading "+tt.what)

			// A missing relay, as for a typo in the realm, is no mesh, not
			// the raw docker failure.
			m = mock.Executor{}
			m.AddResult("", testutil.NoSuchContainer("sind-ssh"), testutil.ExitCode1(t))
			_, err = tt.get(NewManager(docker.NewClient(&m), DefaultRealm), t.Context())
			require.ErrorContains(t, err, `no mesh found for realm "sind"`)
			assert.NotContains(t, err.Error(), "exit status")
		})
	}
}
