// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/slurm"
	"golang.org/x/sync/errgroup"
)

// WorkerRemove removes worker nodes from a cluster.
//
// The caller holds the realm lock (state.LockRealm) until WorkerRemove
// returns.
//
// For managed nodes (those present in sind-nodes.conf), the flow is:
//  1. Read sind-nodes.conf through the controller, which has to run
//  2. Update sind-nodes.conf to remove the node definitions and
//     reconfigure slurmctld, while DNS + known_hosts are deregistered
//  3. Stop + remove containers
//
// For unmanaged nodes, and every node of an unmanaged cluster, step 1 and
// the Slurm part of step 2 are skipped: the Slurm configuration of an
// unmanaged cluster is the user's, even if it contains a sind-nodes.conf.
// So is a configuration without sind-nodes.conf.
func WorkerRemove(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, clusterName string, shortNames []string) error {
	log := sindlog.From(ctx)
	realm := meshMgr.Realm

	if len(shortNames) == 0 {
		return nil
	}

	log.InfoContext(ctx, "removing workers", "cluster", clusterName, "nodes", strings.Join(shortNames, ","))

	// List cluster containers to find controller and validate targets.
	containers, err := client.ListContainers(ctx,
		"label="+LabelRealm+"="+realm,
		"label="+LabelCluster+"="+clusterName)
	if err != nil {
		return fmt.Errorf("listing containers: %w", err)
	}

	controller, hasController := findController(containers, realm, clusterName)
	containerMap := make(map[docker.ContainerName]docker.ContainerListEntry, len(containers))
	for _, c := range containers {
		containerMap[c.Name] = c
	}

	// Resolve which nodes to remove, checking they exist and are worker nodes.
	seen := make(map[string]bool, len(shortNames))
	var targets []docker.ContainerListEntry
	for _, name := range shortNames {
		if seen[name] {
			continue
		}
		seen[name] = true
		cn := ContainerName(realm, clusterName, name)
		c, ok := containerMap[cn]
		if !ok {
			return fmt.Errorf("node %q not found in cluster %q", name, clusterName)
		}
		if config.Role(c.Labels[LabelRole]) != config.RoleWorker {
			return fmt.Errorf("node %q has role %q: only worker nodes can be removed", name, c.Labels[LabelRole])
		}
		targets = append(targets, c)
	}

	// Managed workers leave sind-nodes.conf first. Without a running
	// controller sind cannot edit it, and removing the containers anyway
	// would leave nodes in the Slurm configuration that no container backs.
	// A cluster whose controller is gone has no Slurm to tell.
	var nodesConf string
	updateConf := false
	if hasController && IsManaged(controller.Labels) && slices.ContainsFunc(targets, isManagedContainer) {
		nodesConf, err = readNodesConf(ctx, client, controller)
		switch {
		case errors.Is(err, errSindNodesConfMissing):
			log.DebugContext(ctx, "no sind-nodes.conf, leaving the Slurm configuration alone", "cluster", clusterName)
		case err != nil:
			return err
		default:
			updateConf = true
		}
	}

	// Update Slurm and deregister DNS + known_hosts concurrently, both
	// before the containers go. The group has no shared context, so a
	// failed reconfigure does not cancel a CoreDNS restart halfway.
	// DeregisterMesh logs its failures; worker removal continues.
	var g errgroup.Group
	if updateConf {
		g.Go(func() error {
			return removeNodesConf(ctx, client, controller.Name, nodesConf, shortNames)
		})
	}
	g.Go(func() error {
		return DeregisterMesh(ctx, meshMgr, clusterName, targets)
	})
	if err := g.Wait(); err != nil {
		return err
	}

	// Stop + remove containers.
	return DeleteContainers(ctx, client, targets)
}

// isManagedContainer reports whether sind manages Slurm on a node container
// (see IsManaged).
func isManagedContainer(c docker.ContainerListEntry) bool {
	return IsManaged(c.Labels)
}

// removeNodesConf removes node definitions from sind-nodes.conf and
// reconfigures slurmctld. With none of the nodes in the file, it changes
// nothing and does not reconfigure.
func removeNodesConf(ctx context.Context, client *docker.Client, controllerName docker.ContainerName, currentConf string, shortNames []string) error {
	updated := slurm.RemoveNodesFromConf(currentConf, shortNames)
	if updated == currentConf {
		return nil
	}
	return writeNodesConfAndReconfigure(ctx, client, controllerName, updated)
}
