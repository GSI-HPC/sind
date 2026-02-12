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

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// sshKeygenImage is the container image used as a temporary helper for writing
// SSH keys into the SSH volume. The container is never started.
const sshKeygenImage = "busybox:latest"

// sshKeygenContainerName is the temporary container used during SSH key generation.
const sshKeygenContainerName docker.ContainerName = "sind-ssh-keygen"

// EnsureSSHVolume creates the SSH config volume and generates an ed25519
// keypair if the volume does not already exist. The volume contains
// id_ed25519 (private key), id_ed25519.pub (public key), and an empty
// known_hosts file.
func (m *Manager) EnsureSSHVolume(ctx context.Context) error {
	exists, err := m.Docker.VolumeExists(ctx, cluster.SSHVolumeName)
	if err != nil {
		return fmt.Errorf("checking SSH volume: %w", err)
	}
	if exists {
		return nil
	}

	err = m.Docker.CreateVolume(ctx, cluster.SSHVolumeName)
	if err != nil {
		return fmt.Errorf("creating SSH volume: %w", err)
	}

	privKeyPEM, pubKeyLine := generateKeypair()

	// Write keys to the volume using a temporary container. The container
	// is created (not started) with the volume mounted, files are copied
	// in via docker cp, then the container is removed.
	_, err = m.Docker.CreateContainer(ctx,
		"--name", string(sshKeygenContainerName),
		"-v", string(cluster.SSHVolumeName)+":/ssh",
		sshKeygenImage,
	)
	if err != nil {
		return fmt.Errorf("creating temporary container: %w", err)
	}
	defer m.Docker.RemoveContainer(ctx, sshKeygenContainerName) //nolint:errcheck

	err = m.Docker.CopyToContainer(ctx, sshKeygenContainerName, "/ssh", map[string][]byte{
		"id_ed25519":     privKeyPEM,
		"id_ed25519.pub": pubKeyLine,
		"known_hosts":    {},
	})
	if err != nil {
		return fmt.Errorf("writing SSH keys: %w", err)
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
