// SPDX-License-Identifier: LGPL-3.0-or-later

// Package ssh handles SSH key injection and host key collection for node containers.
package ssh

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/spf13/afero"
)

// authorizedKeysPath is the path to the authorized_keys file inside node containers.
const authorizedKeysPath = "/root/.ssh/authorized_keys"

// InjectPublicKey writes the given SSH public key into the container's
// /root/.ssh/authorized_keys file, creating the directory if needed.
func InjectPublicKey(ctx context.Context, client *docker.Client, container docker.ContainerName, pubKey string) error {
	_, err := client.Exec(ctx, container, "mkdir", "-p", "/root/.ssh")
	if err != nil {
		return fmt.Errorf("creating .ssh directory: %w", err)
	}

	if !strings.HasSuffix(pubKey, "\n") {
		pubKey += "\n"
	}

	err = client.AppendFile(ctx, container, authorizedKeysPath, pubKey)
	if err != nil {
		return fmt.Errorf("writing authorized_keys: %w", err)
	}
	return nil
}

// CollectHostKey retrieves the ed25519 host public key from a node container
// by running ssh-keyscan against localhost. Returns the key in "ssh-ed25519 AAAA..."
// format (without the hostname prefix).
func CollectHostKey(ctx context.Context, client *docker.Client, container docker.ContainerName) (string, error) {
	stdout, err := client.Exec(ctx, container, "ssh-keyscan", "-t", "ed25519", "localhost")
	if err != nil {
		return "", fmt.Errorf("scanning host key: %w", err)
	}

	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Format: "localhost ssh-ed25519 AAAA..."
		_, key, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		return key, nil
	}

	return "", fmt.Errorf("no ed25519 host key found")
}

// defaultRealm is the realm that gets short-name canonicalization in the SSH config.
const defaultRealm = "sind"

// sshConfigTemplate is the SSH config snippet for user SSH client integration.
// Placeholders: realm, SSH container name, dir, dir.
const sshConfigTemplate = `Host *.%[1]s.sind
    ProxyCommand docker exec -i %[2]s bash -c 'exec 3<>/dev/tcp/%%h/22; cat <&3 & cat >&3; kill $!'
    IdentityFile %[3]s/id_ed25519
    UserKnownHostsFile %[3]s/known_hosts
    User root
    StrictHostKeyChecking yes
`

// sshCanonicalTemplate is prepended for the default realm to enable short-name
// resolution via OpenSSH hostname canonicalization. Must appear before any Host
// block so SSH processes canonicalization before matching host patterns.
// Placeholders: realm.
const sshCanonicalTemplate = `CanonicalizeHostname yes
CanonicalDomains default.%[1]s.sind %[1]s.sind
CanonicalizeMaxDots 2

`

// GenerateSSHConfig returns the SSH config snippet pointing to files in dir.
// For the default realm, it includes hostname canonicalization directives
// that enable short-name SSH access (e.g. "ssh controller").
func GenerateSSHConfig(sshContainer docker.ContainerName, dir, realm string) string {
	config := ""
	if realm == defaultRealm {
		config = fmt.Sprintf(sshCanonicalTemplate, realm)
	}
	config += fmt.Sprintf(sshConfigTemplate, realm, string(sshContainer), dir)
	return config
}

// ExportConfig exports SSH configuration to the given directory by reading
// the private key and known_hosts from the SSH relay container and writing
// ssh_config, id_ed25519, and known_hosts to dir.
func ExportConfig(ctx context.Context, client *docker.Client, fs afero.Fs, dir, realm string, sshContainer docker.ContainerName) error {
	privKey, err := client.ReadFile(ctx, sshContainer, "/root/.ssh/id_ed25519")
	if err != nil {
		return fmt.Errorf("reading private key: %w", err)
	}

	knownHosts, err := client.ReadFile(ctx, sshContainer, "/root/.ssh/known_hosts")
	if err != nil {
		return fmt.Errorf("reading known_hosts: %w", err)
	}

	if err := fs.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}

	if err := afero.WriteFile(fs, filepath.Join(dir, "ssh_config"), []byte(GenerateSSHConfig(sshContainer, dir, realm)), 0644); err != nil {
		return fmt.Errorf("writing ssh_config: %w", err)
	}

	if err := afero.WriteFile(fs, filepath.Join(dir, "id_ed25519"), []byte(privKey), 0600); err != nil {
		return fmt.Errorf("writing id_ed25519: %w", err)
	}

	if err := afero.WriteFile(fs, filepath.Join(dir, "known_hosts"), []byte(knownHosts), 0644); err != nil {
		return fmt.Errorf("writing known_hosts: %w", err)
	}

	return nil
}
