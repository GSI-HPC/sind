// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// CreateContainer creates and starts a container in detached mode.
// The args are passed directly to `docker run -d` and should include the
// image name as the last element. Returns the container ID.
func (c *Client) CreateContainer(ctx context.Context, args ...string) (ContainerID, error) {
	runArgs := append([]string{"run", "-d"}, args...)
	stdout, _, err := c.run(ctx, runArgs...)
	if err != nil {
		return "", err
	}
	return ContainerID(strings.TrimSpace(stdout)), nil
}

// StartContainer starts a stopped container.
func (c *Client) StartContainer(ctx context.Context, name ContainerName) error {
	_, _, err := c.run(ctx, "start", string(name))
	return err
}

// StopContainer gracefully stops a running container.
func (c *Client) StopContainer(ctx context.Context, name ContainerName) error {
	_, _, err := c.run(ctx, "stop", string(name))
	return err
}

// RemoveContainer removes a container.
func (c *Client) RemoveContainer(ctx context.Context, name ContainerName) error {
	_, _, err := c.run(ctx, "rm", string(name))
	return err
}

// ContainerInfo holds inspected container details.
type ContainerInfo struct {
	ID     ContainerID
	Name   ContainerName
	Status string
	Labels map[string]string
	IPs    map[NetworkName]string
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
func (c *Client) InspectContainer(ctx context.Context, name ContainerName) (*ContainerInfo, error) {
	stdout, _, err := c.run(ctx, "inspect", string(name))
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
	ips := make(map[NetworkName]string, len(r.NetworkSettings.Networks))
	for net, info := range r.NetworkSettings.Networks {
		ips[NetworkName(net)] = info.IPAddress
	}
	return &ContainerInfo{
		ID:     ContainerID(r.ID),
		Name:   ContainerName(strings.TrimPrefix(r.Name, "/")),
		Status: r.State.Status,
		Labels: r.Config.Labels,
		IPs:    ips,
	}, nil
}

// ContainerListEntry holds summary information from docker ps.
type ContainerListEntry struct {
	ID    ContainerID
	Name  ContainerName
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
			ID:    ContainerID(p.ID),
			Name:  ContainerName(p.Names),
			State: p.State,
			Image: p.Image,
		})
	}
	return entries, nil
}

// Exec runs a command inside a container and returns its stdout.
func (c *Client) Exec(ctx context.Context, container ContainerName, command ...string) (string, error) {
	args := append([]string{"exec", string(container)}, command...)
	stdout, _, err := c.run(ctx, args...)
	if err != nil {
		return "", err
	}
	return stdout, nil
}

// ExecWithStdin runs a command inside a container, piping stdin to it.
func (c *Client) ExecWithStdin(ctx context.Context, container ContainerName, stdin io.Reader, command ...string) error {
	args := append([]string{"exec", "-i", string(container)}, command...)
	_, _, err := c.runWithStdin(ctx, stdin, args...)
	return err
}
