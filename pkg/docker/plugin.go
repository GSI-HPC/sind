// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"strings"
)

// VolumePlugins returns the names of the enabled managed plugins that provide
// a volume driver, as docker plugin ls prints them: with their tag, e.g.
// "cvmfs:latest".
func (c *Client) VolumePlugins(ctx context.Context) ([]string, error) {
	stdout, _, err := c.run(ctx, "plugin", "ls",
		"--filter", "capability=volumedriver",
		"--filter", "enabled=true",
		"--format", "{{.Name}}")
	if err != nil {
		return nil, err
	}
	return strings.Fields(stdout), nil
}
