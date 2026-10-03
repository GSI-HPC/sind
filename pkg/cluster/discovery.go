// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"sort"

	"github.com/GSI-HPC/sind/pkg/docker"
)

// VolumeType identifies a cluster volume kind.
type VolumeType string

// Cluster volume types.
const (
	VolumeConfig VolumeType = "config"
	VolumeMunge  VolumeType = "munge"
	VolumeData   VolumeType = "data"
	// VolumeState holds the slurmctld StateSaveLocation shared by the
	// primary and backup controller. Only clusters with a backup controller
	// have it.
	VolumeState VolumeType = "state"
	// VolumeHome holds the home directories of the cluster users. Only
	// clusters with users have it.
	VolumeHome VolumeType = "home"
)

// AllVolumeTypes lists the cluster volume types in creation order.
var AllVolumeTypes = []VolumeType{VolumeConfig, VolumeMunge, VolumeData, VolumeState, VolumeHome}

// Resources holds the Docker resources belonging to a cluster.
type Resources struct {
	Containers    []docker.ContainerListEntry
	Network       docker.NetworkName
	NetworkExists bool
	Volumes       []docker.VolumeName
}

// ListClusterResources discovers all Docker resources belonging to the named cluster.
// Containers are found by label filter; network and volumes are checked by name
// convention and skipped when their labels name another realm or cluster.
func ListClusterResources(ctx context.Context, client *docker.Client, realm, clusterName string) (*Resources, error) {
	res := &Resources{
		Network: NetworkName(realm, clusterName),
	}

	// Find containers by label. Filter by both realm and cluster so parallel
	// realms with identically-named clusters don't see each other's containers.
	containers, err := client.ListContainers(ctx,
		"label="+LabelRealm+"="+realm,
		"label="+LabelCluster+"="+clusterName)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	res.Containers = containers

	// Check the cluster network and volumes, which are found by name. The
	// names can belong to another realm: realm "ci" with cluster "42-dev"
	// and realm "ci-42" with cluster "dev" both name theirs "ci-42-dev-*".
	labels, exists, err := client.NetworkLabels(ctx, res.Network)
	if err != nil {
		return nil, fmt.Errorf("checking network %s: %w", res.Network, err)
	}
	res.NetworkExists = exists && ownedBy(labels, realm, clusterName)

	for _, vtype := range AllVolumeTypes {
		volName := VolumeName(realm, clusterName, vtype)
		labels, exists, err := client.VolumeLabels(ctx, volName)
		if err != nil {
			return nil, fmt.Errorf("checking volume %s: %w", volName, err)
		}
		if exists && ownedBy(labels, realm, clusterName) {
			res.Volumes = append(res.Volumes, volName)
		}
	}

	return res, nil
}

// ownedBy reports whether a network or volume named after clusterName in
// realm belongs to that cluster: its sind.realm and sind.cluster labels name
// them. Resources without these labels, made by sind before v0.9.0 or by
// docker run for a volume it mounts, count as the cluster's.
func ownedBy(labels docker.Labels, realm, clusterName string) bool {
	r, hasRealm := labels[LabelRealm]
	c, hasCluster := labels[LabelCluster]
	return (!hasRealm || r == realm) && (!hasCluster || c == clusterName)
}

// DiscoverClusterNames finds cluster names from orphaned networks and volumes
// that may not have containers. This supplements GetClusters (which only finds
// clusters with running containers) for cleanup operations.
//
// Filters on both the realm and cluster labels so mesh resources (which carry
// only the realm label) are skipped, and resources from other realms can't
// match even when names collide.
func DiscoverClusterNames(ctx context.Context, client *docker.Client, realm string) ([]string, error) {
	seen := make(map[string]struct{})
	filters := []string{
		"label=" + LabelRealm + "=" + realm,
		"label=" + LabelCluster,
	}

	nets, err := client.ListNetworks(ctx, filters...)
	if err != nil {
		return nil, fmt.Errorf("listing networks: %w", err)
	}
	for _, n := range nets {
		if cluster := n.Labels[LabelCluster]; cluster != "" {
			seen[cluster] = struct{}{}
		}
	}

	vols, err := client.ListVolumes(ctx, filters...)
	if err != nil {
		return nil, fmt.Errorf("listing volumes: %w", err)
	}
	for _, v := range vols {
		if cluster := v.Labels[LabelCluster]; cluster != "" {
			seen[cluster] = struct{}{}
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
//
// A container belongs to clusterName only if its sind.cluster label says so.
// The name prefix "<realm>-<clusterName>-" cannot tell: it also matches the
// containers of cluster "<clusterName>-2". A realm container without a
// cluster label counts as another cluster, which keeps the mesh.
func HasOtherClusters(ctx context.Context, client *docker.Client, realm, clusterName string) (bool, error) {
	containers, err := client.ListContainers(ctx, "label="+LabelRealm+"="+realm)
	if err != nil {
		return false, fmt.Errorf("listing containers: %w", err)
	}
	for _, c := range containers {
		if c.Labels[LabelCluster] != clusterName {
			return true, nil
		}
	}
	return false, nil
}
