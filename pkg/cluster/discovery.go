// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"sort"
	"sync"

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
// The three lookups run concurrently.
func ListClusterResources(ctx context.Context, client *docker.Client, realm, clusterName string) (*Resources, error) {
	res := &Resources{
		Network: NetworkName(realm, clusterName),
	}

	// Find containers by label. Filter by both realm and cluster so parallel
	// realms with identically-named clusters don't see each other's containers.
	//
	// The cluster network and volumes are found by name. The names can
	// belong to another realm: realm "ci" with cluster "42-dev" and realm
	// "ci-42" with cluster "dev" both name theirs "ci-42-dev-*".
	var (
		netLabels docker.Labels
		netExists bool
		volumes   map[docker.VolumeName]docker.Labels
	)
	err := concurrently(
		func() (err error) {
			res.Containers, err = client.ListContainers(ctx,
				"label="+LabelRealm+"="+realm,
				"label="+LabelCluster+"="+clusterName)
			if err != nil {
				return fmt.Errorf("listing containers: %w", err)
			}
			return nil
		},
		func() (err error) {
			netLabels, netExists, err = client.NetworkLabels(ctx, res.Network)
			if err != nil {
				return fmt.Errorf("checking network %s: %w", res.Network, err)
			}
			return nil
		},
		func() (err error) {
			if volumes, err = clusterVolumes(ctx, client, realm, clusterName); err != nil {
				return fmt.Errorf("listing volumes: %w", err)
			}
			return nil
		},
	)
	if err != nil {
		return nil, err
	}

	res.NetworkExists = netExists && ownedBy(netLabels, realm, clusterName)
	for _, vtype := range AllVolumeTypes {
		volName := VolumeName(realm, clusterName, vtype)
		if labels, exists := volumes[volName]; exists && ownedBy(labels, realm, clusterName) {
			res.Volumes = append(res.Volumes, volName)
		}
	}

	return res, nil
}

// clusterVolumes returns the volumes named like the cluster's, with their
// labels, from one docker volume ls filtered by the cluster's name prefix
// instead of an inspect per volume type. The filter is not anchored and
// also matches the volumes of cluster "<clusterName>-2", so callers look
// up exact VolumeName names. Unlabeled volumes, which sind made before
// v0.9.0, are listed too.
func clusterVolumes(ctx context.Context, client *docker.Client, realm, clusterName string) (map[docker.VolumeName]docker.Labels, error) {
	entries, err := client.ListVolumes(ctx, "name="+realm+"-"+clusterName+"-")
	if err != nil {
		return nil, err
	}
	volumes := make(map[docker.VolumeName]docker.Labels, len(entries))
	for _, e := range entries {
		volumes[e.Name] = e.Labels
	}
	return volumes, nil
}

// concurrently runs fns in parallel and waits for all of them. It returns
// the error of the first function, in argument order, that failed, so the
// error does not depend on which call returned first.
func concurrently(fns ...func() error) error {
	errs := make([]error, len(fns))
	var wg sync.WaitGroup
	for i, fn := range fns {
		wg.Go(func() { errs[i] = fn() })
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
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
// match even when names collide. The network and volume listings run
// concurrently.
func DiscoverClusterNames(ctx context.Context, client *docker.Client, realm string) ([]string, error) {
	filters := []string{
		"label=" + LabelRealm + "=" + realm,
		"label=" + LabelCluster,
	}

	var (
		nets []docker.NetworkListEntry
		vols []docker.VolumeListEntry
	)
	err := concurrently(
		func() (err error) {
			if nets, err = client.ListNetworks(ctx, filters...); err != nil {
				return fmt.Errorf("listing networks: %w", err)
			}
			return nil
		},
		func() (err error) {
			if vols, err = client.ListVolumes(ctx, filters...); err != nil {
				return fmt.Errorf("listing volumes: %w", err)
			}
			return nil
		},
	)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{})
	for _, n := range nets {
		if cluster := n.Labels[LabelCluster]; cluster != "" {
			seen[cluster] = struct{}{}
		}
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
