// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"strings"
)

// ServerVersion returns the Docker Engine server version string.
func (c *Client) ServerVersion(ctx context.Context) (string, error) {
	stdout, _, err := c.run(ctx, "version", "--format", "{{.Server.Version}}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout), nil
}

// RunEphemeral runs a command in a temporary container and returns its stdout.
// The container is removed after the command completes (docker run --rm).
// When pull is true, --pull always is added to force a fresh image pull.
func (c *Client) RunEphemeral(ctx context.Context, image string, pull bool, command ...string) (string, error) {
	args := []string{"run", "--rm"}
	if pull {
		args = append(args, "--pull", "always")
	}
	args = append(args, image)
	args = append(args, command...)
	stdout, _, err := c.run(ctx, args...)
	if err != nil {
		return "", err
	}
	return stdout, nil
}
