// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"strings"

	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"golang.org/x/sync/errgroup"
)

// Delete orchestrates the full cluster deletion flow.
//
// Deleting a non-existent cluster is not an error. The function handles
// partial clusters (e.g., from a failed creation) by removing whatever
// resources exist.
//
//	deleteClusterResources
//	      │
//	HasOtherClusters?
//	    yes → done
//	    no  → CleanupMesh
func Delete(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, clusterName string) error {
	log := sindlog.From(ctx)
	realm := meshMgr.Realm

	log.InfoContext(ctx, "deleting cluster", "name", clusterName)

	if err := deleteClusterResources(ctx, client, meshMgr, clusterName); err != nil {
		return err
	}

	// Clean up mesh infrastructure if this was the last cluster.
	hasOther, err := HasOtherClusters(ctx, client, realm, clusterName)
	if err != nil {
		return err
	}
	if !hasOther {
		log.InfoContext(ctx, "last cluster deleted, cleaning up mesh")
		return meshMgr.CleanupMesh(ctx)
	}

	return nil
}

// deleteClusterResources removes containers, network, and volumes for a
// cluster. It also deregisters DNS records and known_hosts entries. Does NOT
// clean up mesh infrastructure — that decision belongs to the caller.
//
// Removing a non-existent cluster is not an error.
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
func deleteClusterResources(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, clusterName string) error {
	log := sindlog.From(ctx)
	realm := meshMgr.Realm

	res, err := ListClusterResources(ctx, client, realm, clusterName)
	if err != nil {
		return err
	}

	// Nothing to delete.
	if len(res.Containers) == 0 && !res.NetworkExists && len(res.Volumes) == 0 {
		log.DebugContext(ctx, "no resources found, nothing to delete")
		return nil
	}

	// Remove DNS records and known_hosts entries before deleting containers.
	if err := DeregisterMesh(ctx, meshMgr, clusterName, res.Containers); err != nil {
		return err
	}

	log.DebugContext(ctx, "removing containers", "count", len(res.Containers))
	if err := DeleteContainers(ctx, client, res.Containers); err != nil {
		return err
	}

	if res.NetworkExists {
		_ = client.DisconnectNetwork(ctx, res.Network, meshMgr.SSHContainerName())
		log.DebugContext(ctx, "removing network", "name", string(res.Network))
		if err := DeleteNetwork(ctx, client, res.Network); err != nil {
			return err
		}
	}

	log.DebugContext(ctx, "removing volumes", "count", len(res.Volumes))
	if err := DeleteVolumes(ctx, client, res.Volumes); err != nil {
		return err
	}

	return nil
}

// DeleteContainers force-removes the given containers in parallel (docker rm -f).
func DeleteContainers(ctx context.Context, client *docker.Client, containers []docker.ContainerListEntry) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, c := range containers {
		g.Go(func() error {
			if err := client.RemoveContainer(gctx, c.Name); err != nil {
				return fmt.Errorf("removing container %s: %w", c.Name, err)
			}
			return nil
		})
	}
	return g.Wait()
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

// DeregisterMesh removes DNS records and known_hosts entries for all
// containers in batch. This is the inverse of registerMesh during cluster
// creation.
func DeregisterMesh(ctx context.Context, meshMgr *mesh.Manager, clusterName string, containers []docker.ContainerListEntry) error {
	if len(containers) == 0 {
		return nil
	}
	prefix := ContainerPrefix(meshMgr.Realm, clusterName)
	hostnames := make([]string, len(containers))
	for i, c := range containers {
		shortName := strings.TrimPrefix(string(c.Name), prefix)
		hostnames[i] = DNSName(shortName, clusterName, meshMgr.Realm)
	}

	if err := meshMgr.RemoveDNSRecords(ctx, hostnames); err != nil {
		return fmt.Errorf("removing DNS records: %w", err)
	}
	if err := meshMgr.RemoveKnownHosts(ctx, hostnames); err != nil {
		return fmt.Errorf("removing known hosts: %w", err)
	}
	return nil
}
