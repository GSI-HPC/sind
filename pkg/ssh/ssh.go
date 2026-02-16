// SPDX-License-Identifier: LGPL-3.0-or-later

// Package ssh handles SSH key injection and host key collection for node containers.
package ssh

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/GSI-HPC/sind/pkg/cluster"
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

// sshConfigTemplate is the SSH config snippet for user SSH client integration.
// The %s placeholders are replaced with the sind directory path.
const sshConfigTemplate = `Host *.sind.local
    ProxyCommand docker exec -i sind-ssh nc %%h 22
    IdentityFile %s/id_ed25519
    UserKnownHostsFile %s/known_hosts
    User root
    StrictHostKeyChecking yes
`

// GenerateSSHConfig returns the SSH config snippet pointing to files in dir.
func GenerateSSHConfig(dir string) string {
	return fmt.Sprintf(sshConfigTemplate, dir, dir)
}

// ExportConfig exports SSH configuration to the given directory by reading
// the private key and known_hosts from the SSH relay container and writing
// ssh_config, id_ed25519, and known_hosts to dir.
func ExportConfig(ctx context.Context, client *docker.Client, fs afero.Fs, dir string) error {
	privKey, err := client.ReadFile(ctx, cluster.SSHContainerName, "/root/.ssh/id_ed25519")
	if err != nil {
		return fmt.Errorf("reading private key: %w", err)
	}

	knownHosts, err := client.ReadFile(ctx, cluster.SSHContainerName, "/root/.ssh/known_hosts")
	if err != nil {
		return fmt.Errorf("reading known_hosts: %w", err)
	}

	if err := fs.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}

	if err := afero.WriteFile(fs, filepath.Join(dir, "ssh_config"), []byte(GenerateSSHConfig(dir)), 0644); err != nil {
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
