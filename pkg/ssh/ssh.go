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
