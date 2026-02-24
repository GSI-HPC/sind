// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"

	"github.com/GSI-HPC/sind/pkg/docker"
)

// volumeTypes lists the cluster volume suffixes in creation order.
var volumeTypes = []string{"config", "munge", "data"}

// Resources holds the Docker resources belonging to a cluster.
type Resources struct {
	Containers    []docker.ContainerListEntry
	Network       docker.NetworkName
	NetworkExists bool
	Volumes       []docker.VolumeName
}

// ListClusterResources discovers all Docker resources belonging to the named cluster.
// Containers are found by label filter; network and volumes are checked by name convention.
func ListClusterResources(ctx context.Context, client *docker.Client, clusterName string) (*Resources, error) {
	res := &Resources{
		Network: NetworkName(clusterName),
	}

	// Find containers by label.
	containers, err := client.ListContainers(ctx,
		"label="+LabelCluster+"="+clusterName)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	res.Containers = containers

	// Check cluster network.
	exists, err := client.NetworkExists(ctx, res.Network)
	if err != nil {
		return nil, fmt.Errorf("checking network %s: %w", res.Network, err)
	}
	res.NetworkExists = exists

	// Check cluster volumes.
	for _, vtype := range volumeTypes {
		volName := VolumeName(clusterName, vtype)
		exists, err := client.VolumeExists(ctx, volName)
		if err != nil {
			return nil, fmt.Errorf("checking volume %s: %w", volName, err)
		}
		if exists {
			res.Volumes = append(res.Volumes, volName)
		}
	}

	return res, nil
}
