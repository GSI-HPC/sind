// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/internal/detach"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"golang.org/x/crypto/ssh"
)

// knownHostsPath is the path to the known_hosts file inside the SSH container.
const knownHostsPath = "/root/.ssh/known_hosts"

// sshVolumeLabel is the com.docker.compose.volume label of the SSH volume.
const sshVolumeLabel = "ssh-config"

// sshImage returns the container image of the SSH relay: sind's default
// node image, which has an ssh client and bash, and which the default
// cluster pulls anyway. Custom node images need not have an ssh client.
func sshImage() string {
	return config.DefaultNodeImage()
}

// ensureSSHVolume creates the SSH config volume if it does not exist yet,
// and reports whether it did. ensureSSH writes the keys into a new volume,
// and into an existing one that lacks them.
//
// An existing volume of that name must be the realm's SSH volume: realm
// a-x's <realm>-ssh-config is also the config volume of cluster x-ssh in
// realm a, which the relay would mount as /root/.ssh. Cluster names ending
// in -ssh are refused (config.CheckName), but a cluster made before that,
// or by another client, can still hold the name. A volume whose sind.realm
// or com.docker.compose.volume label names something else is refused; one
// without them, as docker run makes for a volume it mounts, is not.
func (m *Manager) ensureSSHVolume(ctx context.Context) (bool, error) {
	volName := m.sshVolumeName()
	labels, exists, err := m.Docker.VolumeLabels(ctx, volName)
	if err != nil {
		return false, fmt.Errorf("checking SSH volume: %w", err)
	}
	if exists {
		realm, hasRealm := labels[labelRealm]
		vol, hasVol := labels[docker.ComposeVolumeLabel]
		if hasRealm && realm != m.Realm || hasVol && vol != sshVolumeLabel {
			return false, fmt.Errorf("volume %s is not the SSH volume of realm %q: its labels are %s=%q, %s=%q",
				volName, m.Realm, labelRealm, realm, docker.ComposeVolumeLabel, vol)
		}
		return false, nil
	}

	volumeLabels := docker.Labels{
		labelRealm:                 m.Realm,
		docker.ComposeProjectLabel: m.ComposeProject(),
		docker.ComposeVolumeLabel:  sshVolumeLabel,
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
// it starts. Without writeKeys, a volume that lacks the public key gets them
// too (see lacksKeys).
func (m *Manager) ensureSSH(ctx context.Context, dnsIP string, writeKeys bool) (bool, error) {
	name := m.SSHContainerName()
	info, err := docker.IfExists(m.Docker.InspectContainer(ctx, name))
	if err != nil {
		return false, fmt.Errorf("checking SSH container: %w", err)
	}
	changed := info == nil || info.Status != docker.StateRunning

	if info == nil {
		sshArgs := []string{
			"--name", string(name),
			"--network", string(m.NetworkName()),
			"--dns", dnsIP,
			"-v", string(m.sshVolumeName()) + ":/root/.ssh",
		}
		sshArgs = append(sshArgs, composeLabelFlags(m.ComposeProject(), ComposeServiceSSH)...)
		sshArgs = append(sshArgs, sshImage(), "sleep", "infinity")
		if err := m.pullFor(ctx, sshImage()); err != nil {
			return false, err
		}
		if _, err := m.Docker.CreateContainer(ctx, sshArgs...); err != nil {
			return false, fmt.Errorf("creating SSH container: %w", err)
		}
		info = &docker.ContainerInfo{Name: name, Status: docker.StateCreated}
	}

	if !writeKeys {
		if writeKeys, err = m.lacksKeys(ctx); err != nil {
			return false, err
		}
	}
	if writeKeys {
		changed = true
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

// lacksKeys reports whether the SSH volume lacks the realm's public key, or
// holds an empty one, which it reads through the relay container, running
// or not. An EnsureMesh that was killed, or failed, after it created the
// volume and before it wrote the keys leaves such a volume, which the next
// EnsureMesh finds as an existing one. No node can hold a key that never
// existed, so a new keypair is safe then.
func (m *Manager) lacksKeys(ctx context.Context) (bool, error) {
	key, err := m.Docker.CopyFromContainer(ctx, m.SSHContainerName(), PublicKeyPath)
	if docker.IsFileNotFound(err) {
		err, key = nil, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking SSH keys: %w", err)
	}
	if len(bytes.TrimSpace(key)) > 0 {
		return false, nil
	}
	sindlog.From(ctx).InfoContext(ctx, "SSH volume has no keypair, writing a new one", "volume", string(m.sshVolumeName()))
	return true, nil
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

// PublicKeyPath is the path to the public key inside the SSH container.
const PublicKeyPath = "/root/.ssh/id_ed25519.pub"

// GetSSHPrivateKey reads the SSH private key from the SSH container.
func (m *Manager) GetSSHPrivateKey(ctx context.Context) (string, error) {
	return m.readSSHFile(ctx, privateKeyPath, "SSH private key")
}

// GetSSHPublicKey reads the SSH public key from the SSH container.
func (m *Manager) GetSSHPublicKey(ctx context.Context) (string, error) {
	return m.readSSHFile(ctx, PublicKeyPath, "SSH public key")
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

// KnownHostEntry holds a hostname and its SSH host key for batch registration.
type KnownHostEntry struct {
	Hostname string
	HostKey  string
}

// MissingKnownHosts returns those of hostnames that known_hosts has no
// line of, in their order.
func (m *Manager) MissingKnownHosts(ctx context.Context, hostnames []string) ([]string, error) {
	content, err := m.Docker.ReadFile(ctx, m.SSHContainerName(), knownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("reading known_hosts: %w", err)
	}
	known := make(map[string]bool)
	for line := range strings.SplitSeq(content, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			known[fields[0]] = true
		}
	}
	var missing []string
	for _, h := range hostnames {
		if !known[h] {
			missing = append(missing, h)
		}
	}
	return missing, nil
}

// AddKnownHosts adds host key entries to the known_hosts file. Existing
// entries for the same hostnames are replaced, making the operation
// idempotent on retry.
func (m *Manager) AddKnownHosts(ctx context.Context, entries []KnownHostEntry) error {
	hostnames := make([]string, len(entries))
	lines := make([]string, len(entries))
	for i, e := range entries {
		hostnames[i], lines[i] = e.Hostname, e.Hostname+" "+e.HostKey
	}
	return m.editKnownHosts(ctx, hostnames, lines)
}

// RemoveKnownHosts removes all entries for the given hostnames from the
// known_hosts file in a single operation.
func (m *Manager) RemoveKnownHosts(ctx context.Context, hostnames []string) error {
	return m.editKnownHosts(ctx, hostnames, nil)
}

// RemoveKnownHostsDomains removes the entries of every host in the domains
// from the known_hosts file, as RemoveDNSDomains removes their A records.
// A file without such an entry stays as it is.
func (m *Manager) RemoveKnownHostsDomains(ctx context.Context, domains []string) error {
	content, err := m.Docker.ReadFile(ctx, m.SSHContainerName(), knownHostsPath)
	if err != nil {
		return fmt.Errorf("reading known_hosts: %w", err)
	}
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	kept := withoutDomains(slices.Clone(lines), 0, domains)
	if len(kept) == len(lines) {
		return nil
	}
	var result string
	if len(kept) > 0 {
		result = strings.Join(kept, "\n") + "\n"
	}
	if err := m.writeKnownHosts(ctx, result); err != nil {
		return fmt.Errorf("writing known_hosts: %w", err)
	}
	return nil
}

// editKnownHosts drops the known_hosts lines of hostnames and appends add.
// It does nothing without hostnames.
func (m *Manager) editKnownHosts(ctx context.Context, hostnames, add []string) error {
	if len(hostnames) == 0 {
		return nil
	}
	content, err := m.Docker.ReadFile(ctx, m.SSHContainerName(), knownHostsPath)
	if err != nil {
		return fmt.Errorf("reading known_hosts: %w", err)
	}
	lines := append(withoutNames(strings.Split(content, "\n"), 0, hostnames), add...)
	var result string
	if len(lines) > 0 {
		result = strings.Join(lines, "\n") + "\n"
	}
	if err := m.writeKnownHosts(ctx, result); err != nil {
		return fmt.Errorf("writing known_hosts: %w", err)
	}
	return nil
}

// writeKnownHosts replaces the relay's known_hosts in one step
// (docker.Client.ReplaceFile): a write cut short, even by a Ctrl+C that
// reaches the docker CLI directly, leaves the old file, not a truncated one
// without the host keys of every cluster in the realm. The write ignores
// the cancellation and the deadline of ctx, so that the file keeps up with
// the mesh DNS, but ends with the rollback that ctx runs under
// (internal/detach).
func (m *Manager) writeKnownHosts(ctx context.Context, content string) error {
	ctx, cancel := detach.Context(ctx)
	defer cancel()
	return m.Docker.ReplaceFile(ctx, m.SSHContainerName(), knownHostsPath, content)
}

// generateKeypair creates a new ed25519 keypair and returns the private key
// in OpenSSH PEM format and the public key in authorized_keys format.
func generateKeypair() (privateKey, publicKey []byte) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	block, _ := ssh.MarshalPrivateKey(priv, "")
	sshPub, _ := ssh.NewPublicKey(pub)
	return pem.EncodeToMemory(block), ssh.MarshalAuthorizedKey(sshPub)
}
