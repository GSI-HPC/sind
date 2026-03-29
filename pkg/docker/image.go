// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import "context"

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
