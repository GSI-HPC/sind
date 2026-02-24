// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"

	"github.com/GSI-HPC/sind/pkg/docker"
)

// CreateClusterVolumes creates the config, munge, and data volumes for a cluster.
func CreateClusterVolumes(ctx context.Context, client *docker.Client, clusterName string) error {
	for _, vtype := range []string{"config", "munge", "data"} {
		if err := client.CreateVolume(ctx, VolumeName(clusterName, vtype)); err != nil {
			return fmt.Errorf("creating %s volume: %w", vtype, err)
		}
	}
	return nil
}
