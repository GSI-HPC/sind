// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
)

// volumeTypes lists the cluster volume suffixes in creation order.
var volumeTypes = []string{"config", "munge", "data"}

// Delete orchestrates the full cluster deletion flow.
//
// Deleting a non-existent cluster is not an error. The function handles
// partial clusters (e.g., from a failed creation) by removing whatever
// resources exist.
//
//	ListClusterResources
//	      │
//	DeregisterMesh        DNS + known_hosts per node
//	      │
//	DeleteContainers      stop + rm per container
//	      │
//	DeleteNetwork         rm cluster network
//	      │
//	DeleteVolumes         rm cluster volumes
//	      │
//	HasOtherClusters?
//	    yes → done
//	    no  → CleanupMesh
func Delete(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, clusterName string) error {
	realm := meshMgr.Realm

	res, err := ListClusterResources(ctx, client, realm, clusterName)
	if err != nil {
		return err
	}

	// Nothing to delete.
	if len(res.Containers) == 0 && !res.NetworkExists && len(res.Volumes) == 0 {
		return nil
	}

	// Remove DNS records and known_hosts entries before deleting containers.
	if err := DeregisterMesh(ctx, meshMgr, clusterName, res.Containers); err != nil {
		return err
	}

	if err := DeleteContainers(ctx, client, res.Containers); err != nil {
		return err
	}

	if res.NetworkExists {
		if err := DeleteNetwork(ctx, client, res.Network); err != nil {
			return err
		}
	}

	if err := DeleteVolumes(ctx, client, res.Volumes); err != nil {
		return err
	}

	// Clean up mesh infrastructure if this was the last cluster.
	hasOther, err := HasOtherClusters(ctx, client, realm, clusterName)
	if err != nil {
		return err
	}
	if !hasOther {
		return meshMgr.CleanupMesh(ctx)
	}

	return nil
}

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
	for _, vtype := range volumeTypes {
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
		for _, suffix := range volumeTypes {
			if cluster, ok := strings.CutSuffix(name, "-"+suffix); ok {
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
		return false, fmt.Errorf("listing sind containers: %w", err)
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

// DeregisterMesh removes DNS records and known_hosts entries for each container
// in the cluster. This is the inverse of registerMesh during cluster creation.
func DeregisterMesh(ctx context.Context, meshMgr *mesh.Manager, clusterName string, containers []docker.ContainerListEntry) error {
	prefix := ContainerPrefix(meshMgr.Realm, clusterName)
	for _, c := range containers {
		shortName := strings.TrimPrefix(string(c.Name), prefix)
		dnsName := DNSName(shortName, clusterName)

		if err := meshMgr.RemoveDNSRecord(ctx, dnsName); err != nil {
			return fmt.Errorf("removing DNS record for %s: %w", shortName, err)
		}
		if err := meshMgr.RemoveKnownHost(ctx, dnsName); err != nil {
			return fmt.Errorf("removing known host for %s: %w", shortName, err)
		}
	}
	return nil
}
