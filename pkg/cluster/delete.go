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

// DeleteContainers stops and removes the given containers. Stop errors are
// ignored (the container may already be stopped), but remove errors are fatal.
func DeleteContainers(ctx context.Context, client *docker.Client, containers []docker.ContainerListEntry) error {
	for _, c := range containers {
		_ = client.StopContainer(ctx, c.Name) // best-effort
		if err := client.RemoveContainer(ctx, c.Name); err != nil {
			return fmt.Errorf("removing container %s: %w", c.Name, err)
		}
	}
	return nil
}

// DeleteNetwork removes the cluster network.
func DeleteNetwork(ctx context.Context, client *docker.Client, name docker.NetworkName) error {
	if err := client.RemoveNetwork(ctx, name); err != nil {
		return fmt.Errorf("removing network %s: %w", name, err)
	}
	return nil
}

// DeleteVolumes removes the given cluster volumes.
func DeleteVolumes(ctx context.Context, client *docker.Client, volumes []docker.VolumeName) error {
	for _, v := range volumes {
		if err := client.RemoveVolume(ctx, v); err != nil {
			return fmt.Errorf("removing volume %s: %w", v, err)
		}
	}
	return nil
}
