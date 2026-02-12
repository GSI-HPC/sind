// SPDX-License-Identifier: LGPL-3.0-or-later

// Package docker provides a thin abstraction over the Docker CLI.
package docker

import (
	"context"
	"errors"
	"io"
	"os/exec"
)

// ContainerID is a Docker container identifier (64 hex characters).
type ContainerID string

// ContainerName is a human-readable Docker container name.
type ContainerName string

// NetworkID is a Docker network identifier.
type NetworkID string

// NetworkName is a human-readable Docker network name.
type NetworkName string

// VolumeName is a human-readable Docker volume name.
type VolumeName string

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

func (c *Client) runWithStdin(ctx context.Context, stdin io.Reader, args ...string) (string, string, error) {
	return c.Executor.RunWithStdin(ctx, stdin, c.Command, args...)
}

// exists runs an inspect-style command and returns true if the resource exists.
// Docker inspect commands exit with code 1 for missing resources; other errors
// are propagated.
func (c *Client) exists(ctx context.Context, args ...string) (bool, error) {
	_, _, err := c.run(ctx, args...)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
