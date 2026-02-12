// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
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
