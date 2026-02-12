// SPDX-License-Identifier: LGPL-3.0-or-later

// Package ssh handles SSH key injection and host key collection for node containers.
package ssh

import (
	"context"
	"fmt"
	"strings"

	"github.com/GSI-HPC/sind/pkg/docker"
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
