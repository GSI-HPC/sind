// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"strings"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
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
// starts it when it is stopped. The container runs on the mesh network
// with the SSH volume mounted at /root/.ssh, so that ssh finds the keypair
// and known_hosts, and resolves names through the mesh DNS at dnsIP.
//
// writeKeys generates an ed25519 keypair and writes it, with an empty
// known_hosts, into the volume through the relay: id_ed25519 (private key),
// id_ed25519.pub (public key) and known_hosts. A new relay gets them before
// it starts.
func (m *Manager) ensureSSH(ctx context.Context, dnsIP string, writeKeys bool) error {
	name := m.SSHContainerName()
	info, err := m.inspectIfExists(ctx, name)
	if err != nil {
		return fmt.Errorf("checking SSH container: %w", err)
	}

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
			return fmt.Errorf("creating SSH container: %w", err)
		}
		info = &docker.ContainerInfo{Name: name, Status: docker.StateCreated}
	}

	if writeKeys {
		if err := m.writeSSHKeys(ctx); err != nil {
			return err
		}
	}

	if err := m.startSSH(ctx, info); err != nil {
		return err
	}
	m.WarnStaleDNS(ctx, dnsIP, info)
	return nil
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
	sshName := m.SSHContainerName()
	if err := m.requireMeshContainer(ctx, sshName); err != nil {
		return "", err
	}
	content, err := m.Docker.ReadFile(ctx, sshName, privateKeyPath)
	if err != nil {
		return "", fmt.Errorf("reading SSH private key: %w", err)
	}
	return content, nil
}

// GetSSHPublicKey reads the SSH public key from the SSH container.
func (m *Manager) GetSSHPublicKey(ctx context.Context) (string, error) {
	sshName := m.SSHContainerName()
	if err := m.requireMeshContainer(ctx, sshName); err != nil {
		return "", err
	}
	content, err := m.Docker.ReadFile(ctx, sshName, publicKeyPath)
	if err != nil {
		return "", fmt.Errorf("reading SSH public key: %w", err)
	}
	return content, nil
}

// GetSSHKnownHosts reads the known_hosts file from the SSH container.
func (m *Manager) GetSSHKnownHosts(ctx context.Context) (string, error) {
	sshName := m.SSHContainerName()
	if err := m.requireMeshContainer(ctx, sshName); err != nil {
		return "", err
	}
	content, err := m.Docker.ReadFile(ctx, sshName, knownHostsPath)
	if err != nil {
		return "", fmt.Errorf("reading SSH known_hosts: %w", err)
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
	return marshalOpenSSHPrivateKey(priv, pub), marshalOpenSSHPublicKey(pub)
}

// marshalOpenSSHPublicKey encodes an ed25519 public key in the authorized_keys
// one-line format: "ssh-ed25519 <base64>\n".
func marshalOpenSSHPublicKey(pub ed25519.PublicKey) []byte {
	blob := sshString([]byte("ssh-ed25519"))
	blob = append(blob, sshString([]byte(pub))...)
	return []byte("ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob) + "\n")
}

// marshalOpenSSHPrivateKey encodes an ed25519 key pair in OpenSSH's private
// key format (openssh-key-v1).
func marshalOpenSSHPrivateKey(priv ed25519.PrivateKey, pub ed25519.PublicKey) []byte {
	// Public key section (same wire format as the public key blob).
	pubSection := sshString([]byte("ssh-ed25519"))
	pubSection = append(pubSection, sshString([]byte(pub))...)

	// Private key section with matching check integers.
	check := make([]byte, 4)
	_, _ = rand.Read(check)
	checkInt := binary.BigEndian.Uint32(check)

	var privSection []byte
	privSection = sshUint32(privSection, checkInt)
	privSection = sshUint32(privSection, checkInt)
	privSection = append(privSection, sshString([]byte("ssh-ed25519"))...)
	privSection = append(privSection, sshString([]byte(pub))...)
	privSection = append(privSection, sshString([]byte(priv))...) // full 64-byte key
	privSection = append(privSection, sshString([]byte(""))...)   // comment

	// Pad to block size (8 bytes for "none" cipher).
	for i := byte(1); len(privSection)%8 != 0; i++ {
		privSection = append(privSection, i)
	}

	// Assemble the full binary blob.
	var blob []byte
	blob = append(blob, "openssh-key-v1\x00"...)      // AUTH_MAGIC
	blob = append(blob, sshString([]byte("none"))...) // cipher
	blob = append(blob, sshString([]byte("none"))...) // kdf
	blob = append(blob, sshString([]byte(""))...)     // kdf options
	blob = sshUint32(blob, 1)                         // number of keys
	blob = append(blob, sshString(pubSection)...)     // public key
	blob = append(blob, sshString(privSection)...)    // private key

	return pem.EncodeToMemory(&pem.Block{
		Type:  "OPENSSH PRIVATE KEY",
		Bytes: blob,
	})
}

// sshString encodes data as an SSH wire string (uint32 length prefix + data).
func sshString(data []byte) []byte {
	buf := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(buf, uint32(len(data)))
	copy(buf[4:], data)
	return buf
}

// sshUint32 appends a big-endian uint32 to buf.
func sshUint32(buf []byte, v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return append(buf, b...)
}
