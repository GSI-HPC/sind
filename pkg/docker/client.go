// SPDX-License-Identifier: LGPL-3.0-or-later

// Package docker provides a thin abstraction over the Docker CLI.
package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// ContainerID is a Docker container identifier (64 hex characters).
type ContainerID string

// ContainerName is a human-readable Docker container name.
type ContainerName string

// NetworkID is a Docker network identifier.
type NetworkID string

// NetworkName is a human-readable Docker network name.
type NetworkName string

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

// VolumeName is a human-readable Docker volume name.
type VolumeName string

// CreateVolume creates a Docker volume.
func (c *Client) CreateVolume(ctx context.Context, name VolumeName) error {
	_, _, err := c.run(ctx, "volume", "create", string(name))
	return err
}

// RemoveVolume removes a Docker volume.
func (c *Client) RemoveVolume(ctx context.Context, name VolumeName) error {
	_, _, err := c.run(ctx, "volume", "rm", string(name))
	return err
}

// VolumeListEntry holds summary information from docker volume ls.
type VolumeListEntry struct {
	Name   VolumeName
	Driver string
}

// volumeLsEntry maps the docker volume ls --format json output.
type volumeLsEntry struct {
	Name   string `json:"Name"`
	Driver string `json:"Driver"`
}

// ListVolumes returns volumes matching the given filters.
// Each filter is passed as a --filter flag (e.g. "name=sind-dev").
func (c *Client) ListVolumes(ctx context.Context, filters ...string) ([]VolumeListEntry, error) {
	args := []string{"volume", "ls", "--format", "json"}
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
	var entries []VolumeListEntry
	for _, line := range strings.Split(stdout, "\n") {
		var v volumeLsEntry
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			return nil, fmt.Errorf("parsing volume ls output: %w", err)
		}
		entries = append(entries, VolumeListEntry{
			Name:   VolumeName(v.Name),
			Driver: v.Driver,
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
	_, _, err := c.Executor.RunWithStdin(ctx, stdin, c.Command, args...)
	return err
}

// ImageExists returns true if the given image is available locally.
func (c *Client) ImageExists(ctx context.Context, image string) (bool, error) {
	_, _, err := c.run(ctx, "image", "inspect", image)
	if err != nil {
		// docker image inspect exits 1 for missing images;
		// distinguish from other errors by checking ExitError.
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// RunEphemeral runs a command in a temporary container and returns its stdout.
// The container is removed after the command completes (docker run --rm).
func (c *Client) RunEphemeral(ctx context.Context, image string, command ...string) (string, error) {
	args := append([]string{"run", "--rm", image}, command...)
	stdout, _, err := c.run(ctx, args...)
	if err != nil {
		return "", err
	}
	return stdout, nil
}
