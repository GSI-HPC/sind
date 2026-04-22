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

	"github.com/GSI-HPC/sind/pkg/docker"
)

// knownHostsPath is the path to the known_hosts file inside the SSH container.
const knownHostsPath = "/root/.ssh/known_hosts"

// SSHImage is the container image used for the SSH relay container.
// Uses the sind-node image which includes an ssh client and bash.
const SSHImage = "ghcr.io/gsi-hpc/sind-node:latest"

// sshKeygenImage is the container image used as a temporary helper for writing
// SSH keys into the SSH volume. The container is never started.
const sshKeygenImage = "busybox:latest"

// EnsureSSHVolume creates the SSH config volume and generates an ed25519
// keypair if the volume does not already exist. The volume contains
// id_ed25519 (private key), id_ed25519.pub (public key), and an empty
// known_hosts file.
func (m *Manager) EnsureSSHVolume(ctx context.Context) error {
	volName := m.SSHVolumeName()
	exists, err := m.Docker.VolumeExists(ctx, volName)
	if err != nil {
		return fmt.Errorf("checking SSH volume: %w", err)
	}
	if exists {
		return nil
	}

	volumeLabels := docker.Labels{
		LabelRealm:                 m.Realm,
		docker.ComposeProjectLabel: m.ComposeProject(),
		docker.ComposeVolumeLabel:  "ssh-config",
	}
	err = m.Docker.CreateVolume(ctx, volName, volumeLabels)
	if err != nil {
		return fmt.Errorf("creating SSH volume: %w", err)
	}

	privKeyPEM, pubKeyLine := generateKeypair()

	// Write keys to the volume using a temporary container. The container
	// is created (not started) with the volume mounted, files are copied
	// in via docker cp, then the container is removed.
	keygenName := m.SSHKeygenName()
	keygenArgs := []string{
		"--name", string(keygenName),
		"-v", string(volName) + ":/ssh",
	}
	if m.Pull {
		keygenArgs = append(keygenArgs, "--pull", "always")
	}
	keygenArgs = append(keygenArgs, sshKeygenImage)
	_, err = m.Docker.CreateContainer(ctx, keygenArgs...)
	if err != nil {
		return fmt.Errorf("creating temporary container: %w", err)
	}
	defer m.Docker.RemoveContainer(context.WithoutCancel(ctx), keygenName) //nolint:errcheck

	err = m.Docker.CopyFilesToContainer(ctx, keygenName, "/ssh", map[string]docker.File{
		"id_ed25519":     {Content: privKeyPEM, Mode: 0600},
		"id_ed25519.pub": {Content: pubKeyLine, Mode: 0644},
		"known_hosts":    {Content: nil, Mode: 0644},
	})
	if err != nil {
		return fmt.Errorf("writing SSH keys: %w", err)
	}

	return nil
}

// EnsureSSH creates the SSH relay container if it does not already exist.
// The container runs on the mesh network with the SSH volume mounted at
// /root/.ssh so that ssh automatically discovers the keypair and known_hosts.
func (m *Manager) EnsureSSH(ctx context.Context) error {
	name := m.SSHContainerName()
	exists, err := m.Docker.ContainerExists(ctx, name)
	if err != nil {
		return fmt.Errorf("checking SSH container: %w", err)
	}
	if exists {
		return nil
	}

	// Look up DNS container IP so the SSH container can resolve *.<realm>.sind.
	dnsInfo, err := m.Docker.InspectContainer(ctx, m.DNSContainerName())
	if err != nil {
		return fmt.Errorf("inspecting DNS container for IP: %w", err)
	}
	netName := m.NetworkName()
	dnsIP := dnsInfo.IPs[netName]

	sshArgs := []string{
		"--name", string(name),
		"--network", string(netName),
		"--dns", dnsIP,
		"-v", string(m.SSHVolumeName()) + ":/root/.ssh",
	}
	sshArgs = append(sshArgs, composeLabelFlags(m.ComposeProject(), "ssh")...)
	if m.Pull {
		sshArgs = append(sshArgs, "--pull", "always")
	}
	sshArgs = append(sshArgs, SSHImage, "sleep", "infinity")
	_, err = m.Docker.CreateContainer(ctx, sshArgs...)
	if err != nil {
		return fmt.Errorf("creating SSH container: %w", err)
	}

	err = m.Docker.StartContainer(ctx, name)
	if err != nil {
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
	content, err := m.Docker.ReadFile(ctx, m.SSHContainerName(), privateKeyPath)
	if err != nil {
		return "", fmt.Errorf("reading SSH private key: %w", err)
	}
	return content, nil
}

// GetSSHPublicKey reads the SSH public key from the SSH container.
func (m *Manager) GetSSHPublicKey(ctx context.Context) (string, error) {
	content, err := m.Docker.ReadFile(ctx, m.SSHContainerName(), publicKeyPath)
	if err != nil {
		return "", fmt.Errorf("reading SSH public key: %w", err)
	}
	return content, nil
}

// GetSSHKnownHosts reads the known_hosts file from the SSH container.
func (m *Manager) GetSSHKnownHosts(ctx context.Context) (string, error) {
	content, err := m.Docker.ReadFile(ctx, m.SSHContainerName(), knownHostsPath)
	if err != nil {
		return "", fmt.Errorf("reading SSH known_hosts: %w", err)
	}
	return content, nil
}

// AddKnownHost appends a host key entry to the known_hosts file in the SSH
// container. The hostKey should be the full key type and data (e.g.
// "ssh-ed25519 AAAA...").
func (m *Manager) AddKnownHost(ctx context.Context, hostname, hostKey string) error {
	entry := hostname + " " + hostKey + "\n"
	err := m.Docker.AppendFile(ctx, m.SSHContainerName(), knownHostsPath, entry)
	if err != nil {
		return fmt.Errorf("adding known host %s: %w", hostname, err)
	}
	return nil
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

	err = m.Docker.WriteFile(ctx, name, knownHostsPath, buf.String())
	if err != nil {
		return fmt.Errorf("writing known_hosts: %w", err)
	}
	return nil
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

	err = m.Docker.WriteFile(ctx, name, knownHostsPath, result)
	if err != nil {
		return fmt.Errorf("writing known_hosts: %w", err)
	}

	return nil
}

// RemoveKnownHost removes all entries for the given hostname from the
// known_hosts file in the SSH container.
func (m *Manager) RemoveKnownHost(ctx context.Context, hostname string) error {
	name := m.SSHContainerName()
	content, err := m.Docker.ReadFile(ctx, name, knownHostsPath)
	if err != nil {
		return fmt.Errorf("reading known_hosts: %w", err)
	}

	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 1 && fields[0] == hostname {
			continue
		}
		kept = append(kept, line)
	}

	var result string
	if len(kept) > 0 {
		result = strings.Join(kept, "\n") + "\n"
	}

	err = m.Docker.WriteFile(ctx, name, knownHostsPath, result)
	if err != nil {
		return fmt.Errorf("writing known_hosts: %w", err)
	}

	return nil
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
