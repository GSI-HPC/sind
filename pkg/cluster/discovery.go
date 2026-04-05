// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/GSI-HPC/sind/pkg/docker"
)

// VolumeType identifies a cluster volume kind.
type VolumeType string

// Cluster volume types.
const (
	VolumeConfig VolumeType = "config"
	VolumeMunge  VolumeType = "munge"
	VolumeData   VolumeType = "data"
)

// AllVolumeTypes lists the cluster volume types in creation order.
var AllVolumeTypes = []VolumeType{VolumeConfig, VolumeMunge, VolumeData}

// Resources holds the Docker resources belonging to a cluster.
type Resources struct {
	Containers    []docker.ContainerListEntry
	Network       docker.NetworkName
	NetworkExists bool
	Volumes       []docker.VolumeName
}

// ListClusterResources discovers all Docker resources belonging to the named cluster.
// Containers are found by label filter; network and volumes are checked by name convention.
func ListClusterResources(ctx context.Context, client *docker.Client, realm, clusterName string) (*Resources, error) {
	res := &Resources{
		Network: NetworkName(realm, clusterName),
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
	for _, vtype := range AllVolumeTypes {
		volName := VolumeName(realm, clusterName, vtype)
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

// DiscoverClusterNames finds cluster names from orphaned networks and volumes
// that may not have containers. This supplements GetClusters (which only finds
// clusters with running containers) for cleanup operations.
func DiscoverClusterNames(ctx context.Context, client *docker.Client, realm string) ([]string, error) {
	seen := make(map[string]struct{})
	prefix := realm + "-"

	// Extract cluster names from networks: <realm>-<cluster>-net
	nets, err := client.ListNetworks(ctx, "name="+prefix)
	if err != nil {
		return nil, fmt.Errorf("listing networks: %w", err)
	}
	for _, n := range nets {
		name := strings.TrimPrefix(string(n.Name), prefix)
		if cluster, ok := strings.CutSuffix(name, "-net"); ok {
			seen[cluster] = struct{}{}
		}
	}

	// Extract cluster names from volumes: <realm>-<cluster>-{config,munge,data}
	// Skip mesh volumes like <realm>-ssh-config which aren't cluster resources.
	meshVolSuffix := "ssh-config"
	vols, err := client.ListVolumes(ctx, "name="+prefix)
	if err != nil {
		return nil, fmt.Errorf("listing volumes: %w", err)
	}
	for _, v := range vols {
		name := strings.TrimPrefix(string(v.Name), prefix)
		if name == meshVolSuffix {
			continue
		}
		for _, suffix := range AllVolumeTypes {
			if cluster, ok := strings.CutSuffix(name, "-"+string(suffix)); ok {
				seen[cluster] = struct{}{}
				break
			}
		}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// HasOtherClusters checks whether any sind cluster containers exist besides
// the named cluster. This is used to decide whether to clean up mesh
// infrastructure after deleting a cluster.
func HasOtherClusters(ctx context.Context, client *docker.Client, realm, clusterName string) (bool, error) {
	containers, err := client.ListContainers(ctx, "label="+LabelRealm+"="+realm)
	if err != nil {
		return false, fmt.Errorf("listing containers: %w", err)
	}
	for _, c := range containers {
		// Check if any container belongs to a different cluster by inspecting
		// the container name prefix. Containers for clusterName have the
		// prefix "<realm>-<clusterName>-".
		prefix := ContainerPrefix(realm, clusterName)
		if !strings.HasPrefix(string(c.Name), prefix) {
			return true, nil
		}
	}
	return false, nil
}
