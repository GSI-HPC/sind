// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"errors"
	"os/exec"
)

// ImageExists returns true if the given image is available locally.
func (c *Client) ImageExists(ctx context.Context, image string) (bool, error) {
	_, _, err := c.run(ctx, "image", "inspect", image)
	if err != nil {
		// docker image inspect exits 1 for missing images;
		// distinguish from other errors by checking ExitError.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
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
