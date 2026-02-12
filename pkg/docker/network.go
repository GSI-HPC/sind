// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// NetworkExists returns true if the given network exists.
func (c *Client) NetworkExists(ctx context.Context, name NetworkName) (bool, error) {
	_, _, err := c.run(ctx, "network", "inspect", string(name))
	if err != nil {
		// docker network inspect exits 1 for missing networks;
		// distinguish from other errors by checking ExitError.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// CreateNetwork creates a Docker network and returns its ID.
func (c *Client) CreateNetwork(ctx context.Context, name NetworkName) (NetworkID, error) {
	stdout, _, err := c.run(ctx, "network", "create", string(name))
	if err != nil {
		return "", err
	}
	return NetworkID(strings.TrimSpace(stdout)), nil
}

// RemoveNetwork removes a Docker network.
func (c *Client) RemoveNetwork(ctx context.Context, name NetworkName) error {
	_, _, err := c.run(ctx, "network", "rm", string(name))
	return err
}

// ConnectNetwork connects a container to a network.
func (c *Client) ConnectNetwork(ctx context.Context, network NetworkName, container ContainerName) error {
	_, _, err := c.run(ctx, "network", "connect", string(network), string(container))
	return err
}

// DisconnectNetwork disconnects a container from a network.
func (c *Client) DisconnectNetwork(ctx context.Context, network NetworkName, container ContainerName) error {
	_, _, err := c.run(ctx, "network", "disconnect", string(network), string(container))
	return err
}
