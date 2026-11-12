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
)

// WorkerRemove removes worker nodes from a cluster.
//
// The caller holds the realm lock (state.LockRealm) until WorkerRemove
// returns.
//
// For managed nodes (those present in sind-nodes.conf), the flow is:
//  1. Read sind-nodes.conf through the controller, which has to run
//  2. Update sind-nodes.conf to remove the node definitions and
//     reconfigure slurmctld
//  3. Deregister DNS + known_hosts
//  4. Stop + remove containers
//  5. On a monitored cluster, sync the monitoring stack and remove the
//     workers' journal directories (see forgetMonitoredNodes)
//
// For unmanaged nodes, and every node of an unmanaged cluster, steps 1 and
// 2 are skipped: the Slurm configuration of an unmanaged cluster is the
// user's, even if it contains a sind-nodes.conf. So is a configuration
// without sind-nodes.conf.
//
// The steps it reports are "slurm" (the update of sind-nodes.conf and the
// reconfigure), then "mesh deregistration", "nodes" with a target for each
// worker, and "monitoring" on a monitored cluster.
func WorkerRemove(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, clusterName string, shortNames []string) error {
	log := sindlog.From(ctx)
	realm := meshMgr.Realm

	if len(shortNames) == 0 {
		return nil
	}

	log.InfoContext(ctx, "removing workers", "cluster", clusterName, "nodes", strings.Join(shortNames, ","))

	// List cluster containers to find controller and validate targets.
	containers, err := existingClusterContainers(ctx, client, realm, clusterName)
	if err != nil {
		return err
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
			return errorWith(ErrNodeNotFound, "node %q not found in cluster %q", name, clusterName)
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
		case errors.Is(err, ErrNodesConfMissing):
			log.DebugContext(ctx, "no sind-nodes.conf, leaving the Slurm configuration alone", "cluster", clusterName)
		case err != nil:
			return err
		default:
			updateConf = true
		}
	}

	// Update Slurm, then deregister DNS + known_hosts, both before the
	// containers go. A failed update keeps the workers, and with them their
	// DNS records and known_hosts entries, so that they stay reachable.
	// DeregisterMesh logs its failures; worker removal continues.
	if updateConf {
		sctx, end := startStep(ctx, "slurm")
		err = removeNodesConf(sctx, client, controller.Name, nodesConf, shortNames)
		end(err)
		if err != nil {
			return err
		}
	}
	DeregisterMesh(ctx, meshMgr, clusterName, targets)

	// Which of the workers the monitoring stack collects is told by their
	// containers, so before they go.
	monitored, err := monitoredContainers(ctx, client, realm, clusterName)
	if err != nil {
		return err
	}
	var forget []string
	for _, c := range targets {
		if slices.ContainsFunc(monitored, func(m docker.ContainerListEntry) bool { return m.Name == c.Name }) {
			forget = append(forget, strings.TrimPrefix(string(c.Name), ContainerPrefix(realm, clusterName)))
		}
	}

	// Stop + remove containers.
	if err := DeleteContainers(ctx, client, targets); err != nil {
		return err
	}
	if len(forget) > 0 {
		forgetMonitoredNodes(ctx, client, meshMgr, clusterName, forget...)
	}
	return nil
}

// isManagedContainer reports whether sind manages Slurm on a node container
// (see IsManaged).
func isManagedContainer(c docker.ContainerListEntry) bool {
	return IsManaged(c.Labels)
}

// removeNodesConf removes node definitions from sind-nodes.conf and
// reconfigures slurmctld. With none of the nodes in the file, it changes
// nothing and does not reconfigure. When the update fails, it writes
// currentConf back: the containers then stay, and a retry finds the nodes
// in the file again and reconfigures slurmctld, which a file without them
// would skip.
func removeNodesConf(ctx context.Context, client *docker.Client, controllerName docker.ContainerName, currentConf string, shortNames []string) error {
	updated := slurm.RemoveNodesFromConf(currentConf, shortNames)
	if updated == currentConf {
		return nil
	}
	err := writeNodesConfAndReconfigure(ctx, client, controllerName, updated)
	if err == nil {
		return nil
	}
	if rerr := replaceNodesConf(ctx, client, controllerName, currentConf); rerr != nil {
		return errors.Join(err, fmt.Errorf("restoring sind-nodes.conf: %w", rerr))
	}
	return err
}
