// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// NetworkExists returns true if the given network exists.
func (c *Client) NetworkExists(ctx context.Context, name NetworkName) (bool, error) {
	return c.exists(ctx, "network", "inspect", string(name))
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

// NetworkListEntry holds summary information from docker network ls.
type NetworkListEntry struct {
	Name   NetworkName
	Driver string
}

// networkLsEntry maps the docker network ls --format json output.
type networkLsEntry struct {
	Name   string `json:"Name"`
	Driver string `json:"Driver"`
}

// ListNetworks returns networks matching the given filters.
// Each filter is passed as a --filter flag (e.g. "name=sind").
func (c *Client) ListNetworks(ctx context.Context, filters ...string) ([]NetworkListEntry, error) {
	args := []string{"network", "ls", "--format", "json"}
	for _, f := range filters {
		args = append(args, "--filter", f)
	}
	stdout, _, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		return nil, nil
	}
	var entries []NetworkListEntry
	for _, line := range strings.Split(stdout, "\n") {
		var n networkLsEntry
		if err := json.Unmarshal([]byte(line), &n); err != nil {
			return nil, fmt.Errorf("parsing network ls output: %w", err)
		}
		entries = append(entries, NetworkListEntry{
			Name:   NetworkName(n.Name),
			Driver: n.Driver,
		})
	}
	return entries, nil
}
