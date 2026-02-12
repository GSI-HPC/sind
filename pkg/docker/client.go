// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Client provides operations against the Docker CLI.
type Client struct {
	Executor Executor
	Command  string // docker executable path (default: "docker")
}

// NewClient returns a Client that runs docker commands through the given executor.
func NewClient(executor Executor) *Client {
	return &Client{Executor: executor, Command: "docker"}
}

func (c *Client) run(ctx context.Context, args ...string) (string, string, error) {
	return c.Executor.Run(ctx, c.Command, args...)
}

// CreateContainer creates and starts a container in detached mode.
// The args are passed directly to `docker run -d` and should include the
// image name as the last element. Returns the container ID.
func (c *Client) CreateContainer(ctx context.Context, args ...string) (string, error) {
	runArgs := append([]string{"run", "-d"}, args...)
	stdout, _, err := c.run(ctx, runArgs...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout), nil
}

// StartContainer starts a stopped container.
func (c *Client) StartContainer(ctx context.Context, name string) error {
	_, _, err := c.run(ctx, "start", name)
	return err
}

// StopContainer gracefully stops a running container.
func (c *Client) StopContainer(ctx context.Context, name string) error {
	_, _, err := c.run(ctx, "stop", name)
	return err
}

// RemoveContainer removes a container.
func (c *Client) RemoveContainer(ctx context.Context, name string) error {
	_, _, err := c.run(ctx, "rm", name)
	return err
}

// ContainerInfo holds inspected container details.
type ContainerInfo struct {
	ID     string
	Name   string
	Status string
	Labels map[string]string
	IPs    map[string]string // network name → IP address
}

// inspectResult maps the subset of docker inspect JSON we care about.
type inspectResult struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	State struct {
		Status string `json:"Status"`
	} `json:"State"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// InspectContainer returns detailed information about a container.
func (c *Client) InspectContainer(ctx context.Context, name string) (*ContainerInfo, error) {
	stdout, _, err := c.run(ctx, "inspect", name)
	if err != nil {
		return nil, err
	}
	var results []inspectResult
	if err := json.Unmarshal([]byte(stdout), &results); err != nil {
		return nil, fmt.Errorf("parsing inspect output: %w", err)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("inspect returned no results for %q", name)
	}
	r := results[0]
	ips := make(map[string]string, len(r.NetworkSettings.Networks))
	for net, info := range r.NetworkSettings.Networks {
		ips[net] = info.IPAddress
	}
	return &ContainerInfo{
		ID:     r.ID,
		Name:   strings.TrimPrefix(r.Name, "/"),
		Status: r.State.Status,
		Labels: r.Config.Labels,
		IPs:    ips,
	}, nil
}

// ContainerListEntry holds summary information from docker ps.
type ContainerListEntry struct {
	ID    string
	Name  string
	State string
	Image string
}

// psEntry maps the docker ps --format json output.
type psEntry struct {
	ID    string `json:"ID"`
	Names string `json:"Names"`
	State string `json:"State"`
	Image string `json:"Image"`
}

// ListContainers returns containers matching the given filters.
// Each filter is passed as a --filter flag to docker ps (e.g. "label=sind.cluster").
func (c *Client) ListContainers(ctx context.Context, filters ...string) ([]ContainerListEntry, error) {
	args := []string{"ps", "-a", "--no-trunc", "--format", "json"}
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
	var entries []ContainerListEntry
	for _, line := range strings.Split(stdout, "\n") {
		var p psEntry
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			return nil, fmt.Errorf("parsing ps output: %w", err)
		}
		entries = append(entries, ContainerListEntry{
			ID:    p.ID,
			Name:  p.Names,
			State: p.State,
			Image: p.Image,
		})
	}
	return entries, nil
}

// CreateNetwork creates a Docker network and returns its ID.
func (c *Client) CreateNetwork(ctx context.Context, name string) (string, error) {
	stdout, _, err := c.run(ctx, "network", "create", name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout), nil
}

// RemoveNetwork removes a Docker network.
func (c *Client) RemoveNetwork(ctx context.Context, name string) error {
	_, _, err := c.run(ctx, "network", "rm", name)
	return err
}

// ConnectNetwork connects a container to a network.
func (c *Client) ConnectNetwork(ctx context.Context, network, container string) error {
	_, _, err := c.run(ctx, "network", "connect", network, container)
	return err
}

// DisconnectNetwork disconnects a container from a network.
func (c *Client) DisconnectNetwork(ctx context.Context, network, container string) error {
	_, _, err := c.run(ctx, "network", "disconnect", network, container)
	return err
}
