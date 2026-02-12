// SPDX-License-Identifier: LGPL-3.0-or-later

package probe

import (
	"context"
	"fmt"

	"github.com/GSI-HPC/sind/pkg/docker"
)

// CheckContainerRunning verifies that the container is in the "running" state.
func CheckContainerRunning(ctx context.Context, client *docker.Client, name docker.ContainerName) error {
	info, err := client.InspectContainer(ctx, name)
	if err != nil {
		return fmt.Errorf("inspecting container: %w", err)
	}
	if info.Status != "running" {
		return fmt.Errorf("container %s is %s, expected running", name, info.Status)
	}
	return nil
}
