// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"strings"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"golang.org/x/crypto/ssh"
)

// knownHostsPath is the path to the known_hosts file inside the SSH container.
const knownHostsPath = "/root/.ssh/known_hosts"

// SSHImage returns the container image of the SSH relay: sind's default
// node image, which has an ssh client and bash, and which the default
// cluster pulls anyway. Custom node images need not have an ssh client.
func SSHImage() string {
	return config.DefaultImage
}

// ensureSSHVolume creates the SSH config volume if it does not exist yet,
// and reports whether it did. ensureSSH writes the keys into a new volume.
func (m *Manager) ensureSSHVolume(ctx context.Context) (bool, error) {
	volName := m.SSHVolumeName()
	exists, err := m.Docker.VolumeExists(ctx, volName)
	if err != nil {
		return false, fmt.Errorf("checking SSH volume: %w", err)
	}
	if exists {
		return false, nil
	}

	volumeLabels := docker.Labels{
		LabelRealm:                 m.Realm,
		docker.ComposeProjectLabel: m.ComposeProject(),
		docker.ComposeVolumeLabel:  "ssh-config",
	}
	if err := m.Docker.CreateVolume(ctx, volName, volumeLabels); err != nil {
		return false, fmt.Errorf("creating SSH volume: %w", err)
	}
	return true, nil
}

// ensureSSH creates the SSH relay container if it does not exist yet, or
// starts it when it is stopped, and reports whether it changed the relay.
// The container runs on the mesh network with the SSH volume mounted at
// /root/.ssh, so that ssh finds the keypair and known_hosts, and resolves
// names through the mesh DNS at dnsIP.
//
// writeKeys generates an ed25519 keypair and writes it, with an empty
// known_hosts, into the volume through the relay: id_ed25519 (private key),
// id_ed25519.pub (public key) and known_hosts. A new relay gets them before
// it starts.
func (m *Manager) ensureSSH(ctx context.Context, dnsIP string, writeKeys bool) (bool, error) {
	name := m.SSHContainerName()
	info, err := docker.IfExists(m.Docker.InspectContainer(ctx, name))
	if err != nil {
		return false, fmt.Errorf("checking SSH container: %w", err)
	}
	changed := info == nil || writeKeys || info.Status != docker.StateRunning

	if info == nil {
		sshArgs := []string{
			"--name", string(name),
			"--network", string(m.NetworkName()),
			"--dns", dnsIP,
			"-v", string(m.SSHVolumeName()) + ":/root/.ssh",
		}
		sshArgs = append(sshArgs, composeLabelFlags(m.ComposeProject(), "ssh")...)
		if m.Pull {
			sshArgs = append(sshArgs, "--pull", "always")
		}
		sshArgs = append(sshArgs, SSHImage(), "sleep", "infinity")
		if _, err := m.Docker.CreateContainer(ctx, sshArgs...); err != nil {
			return false, fmt.Errorf("creating SSH container: %w", err)
		}
		info = &docker.ContainerInfo{Name: name, Status: docker.StateCreated}
	}

	if writeKeys {
		if err := m.writeSSHKeys(ctx); err != nil {
			return false, err
		}
	}

	if err := m.startSSH(ctx, info); err != nil {
		return false, err
	}
	m.WarnStaleDNS(ctx, dnsIP, info)
	return changed, nil
}

// writeSSHKeys generates the realm's keypair and copies it, with an empty
// known_hosts, into the SSH volume through the relay container, which may be
// running or not started yet.
func (m *Manager) writeSSHKeys(ctx context.Context) error {
	privKeyPEM, pubKeyLine := generateKeypair()
	err := m.Docker.CopyFilesToContainer(ctx, m.SSHContainerName(), "/root/.ssh", map[string]docker.File{
		"id_ed25519":     {Content: privKeyPEM, Mode: 0600},
		"id_ed25519.pub": {Content: pubKeyLine, Mode: 0644},
		"known_hosts":    {Content: nil, Mode: 0644},
	})
	if err != nil {
		return fmt.Errorf("writing SSH keys: %w", err)
	}
	return nil
}

// startSSH starts the SSH relay container that info describes unless it is
// running.
func (m *Manager) startSSH(ctx context.Context, info *docker.ContainerInfo) error {
	if info.Status == docker.StateRunning {
		return nil
	}
	sindlog.From(ctx).InfoContext(ctx, "starting SSH relay", "container", string(info.Name), "state", string(info.Status))
	if err := m.Docker.StartContainer(ctx, info.Name); err != nil {
		return fmt.Errorf("starting SSH container: %w", err)
	}
	return nil
}

// privateKeyPath is the path to the private key inside the SSH container.
const privateKeyPath = "/root/.ssh/id_ed25519"

// publicKeyPath is the path to the public key inside the SSH container.
const publicKeyPath = "/root/.ssh/id_ed25519.pub"

// GetSSHPrivateKey reads the SSH private key from the SSH container.
func (m *Manager) GetSSHPrivateKey(ctx context.Context) (string, error) {
	return m.readSSHFile(ctx, privateKeyPath, "SSH private key")
}

// GetSSHPublicKey reads the SSH public key from the SSH container.
func (m *Manager) GetSSHPublicKey(ctx context.Context) (string, error) {
	return m.readSSHFile(ctx, publicKeyPath, "SSH public key")
}

// GetSSHKnownHosts reads the known_hosts file from the SSH container.
func (m *Manager) GetSSHKnownHosts(ctx context.Context) (string, error) {
	return m.readSSHFile(ctx, knownHostsPath, "SSH known_hosts")
}

// readSSHFile reads the file at path, which holds what, from the SSH
// container.
func (m *Manager) readSSHFile(ctx context.Context, path, what string) (string, error) {
	if err := m.requireMeshContainer(ctx, m.SSHContainerName()); err != nil {
		return "", err
	}
	content, err := m.Docker.ReadFile(ctx, m.SSHContainerName(), path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", what, err)
	}
	return content, nil
}

// AddKnownHost adds a host key entry to the known_hosts file in the SSH
// container, replacing an existing entry for the hostname, as AddKnownHosts
// does. The hostKey should be the full key type and data (e.g.
// "ssh-ed25519 AAAA...").
func (m *Manager) AddKnownHost(ctx context.Context, hostname, hostKey string) error {
	return m.AddKnownHosts(ctx, []KnownHostEntry{{Hostname: hostname, HostKey: hostKey}})
}

// KnownHostEntry holds a hostname and its SSH host key for batch registration.
type KnownHostEntry struct {
	Hostname string
	HostKey  string
}

// AddKnownHosts adds host key entries to the known_hosts file. Existing
// entries for the same hostnames are replaced, making the operation
// idempotent on retry.
func (m *Manager) AddKnownHosts(ctx context.Context, entries []KnownHostEntry) error {
	if len(entries) == 0 {
		return nil
	}
	name := m.SSHContainerName()
	content, err := m.Docker.ReadFile(ctx, name, knownHostsPath)
	if err != nil {
		return fmt.Errorf("reading known_hosts: %w", err)
	}

	// Build set of hostnames being added for dedup.
	newHostnames := make(map[string]bool, len(entries))
	for _, e := range entries {
		newHostnames[e.Hostname] = true
	}

	// Keep existing lines that don't conflict with new entries.
	lines := strings.Split(content, "\n")
	var buf strings.Builder
	for _, line := range lines {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 1 && newHostnames[fields[0]] {
			continue
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	for _, e := range entries {
		buf.WriteString(e.Hostname)
		buf.WriteByte(' ')
		buf.WriteString(e.HostKey)
		buf.WriteByte('\n')
	}

	err = m.writeKnownHosts(ctx, buf.String())
	if err != nil {
		return fmt.Errorf("writing known_hosts: %w", err)
	}
	return nil
}

// writeKnownHosts replaces the relay's known_hosts. The write ignores the
// cancellation of ctx: `cat >` truncates the file first, and a write cut
// short by Ctrl+C would drop the host keys of every cluster in the realm.
func (m *Manager) writeKnownHosts(ctx context.Context, content string) error {
	return m.Docker.WriteFile(context.WithoutCancel(ctx), m.SSHContainerName(), knownHostsPath, content)
}

// RemoveKnownHosts removes all entries for the given hostnames from the
// known_hosts file in a single operation.
func (m *Manager) RemoveKnownHosts(ctx context.Context, hostnames []string) error {
	if len(hostnames) == 0 {
		return nil
	}
	name := m.SSHContainerName()
	content, err := m.Docker.ReadFile(ctx, name, knownHostsPath)
	if err != nil {
		return fmt.Errorf("reading known_hosts: %w", err)
	}

	remove := make(map[string]bool, len(hostnames))
	for _, h := range hostnames {
		remove[h] = true
	}

	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 1 && remove[fields[0]] {
			continue
		}
		kept = append(kept, line)
	}

	var result string
	if len(kept) > 0 {
		result = strings.Join(kept, "\n") + "\n"
	}

	err = m.writeKnownHosts(ctx, result)
	if err != nil {
		return fmt.Errorf("writing known_hosts: %w", err)
	}

	return nil
}

// RemoveKnownHost removes all entries for the given hostname from the
// known_hosts file in the SSH container.
func (m *Manager) RemoveKnownHost(ctx context.Context, hostname string) error {
	return m.RemoveKnownHosts(ctx, []string{hostname})
}

// generateKeypair creates a new ed25519 keypair and returns the private key
// in OpenSSH PEM format and the public key in authorized_keys format.
func generateKeypair() (privateKey, publicKey []byte) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	block, _ := ssh.MarshalPrivateKey(priv, "")
	sshPub, _ := ssh.NewPublicKey(pub)
	return pem.EncodeToMemory(block), ssh.MarshalAuthorizedKey(sshPub)
}
