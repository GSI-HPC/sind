// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"strings"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// WorkerRemove removes worker nodes from a cluster.
//
// For managed nodes (those present in sind-nodes.conf), the flow is:
//  1. Update sind-nodes.conf to remove the node definitions
//  2. Reconfigure slurmctld
//  3. Deregister DNS + known_hosts
//  4. Stop + remove containers
//
// For unmanaged nodes, only steps 3–4 are performed.
func WorkerRemove(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, clusterName string, shortNames []string) error {
	log := sindlog.From(ctx)
	realm := meshMgr.Realm

	if len(shortNames) == 0 {
		return nil
	}

	log.InfoContext(ctx, "removing workers", "cluster", clusterName, "nodes", strings.Join(shortNames, ","))

	// List cluster containers to find controller and validate targets.
	containers, err := client.ListContainers(ctx, "label="+LabelCluster+"="+clusterName)
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
			return fmt.Errorf("node %q has role %q: only worker nodes can be removed with worker remove", name, c.Labels[LabelRole])
		}
		targets = append(targets, c)
	}

	// For managed nodes: check if sind-nodes.conf exists and update it.
	if hasController {
		nodesConf, err := client.ReadFile(ctx, controller.Name, slurm.NodesConfPath)
		if err == nil {
			// sind-nodes.conf exists → remove managed nodes from it.
			if err := removeNodesConf(ctx, client, controller.Name, nodesConf, shortNames); err != nil {
				return err
			}
		}
		// If ReadFile fails, sind-nodes.conf doesn't exist → treat as unmanaged.
	}

	// Deregister DNS + known_hosts.
	if err := DeregisterMesh(ctx, meshMgr, clusterName, targets); err != nil {
		return err
	}

	// Stop + remove containers.
	return DeleteContainers(ctx, client, targets)
}

// removeNodesConf removes node definitions from sind-nodes.conf and
// reconfigures slurmctld.
func removeNodesConf(ctx context.Context, client *docker.Client, controllerName docker.ContainerName, currentConf string, shortNames []string) error {
	updated := slurm.RemoveNodesFromConf(currentConf, shortNames)
	return writeNodesConfAndReconfigure(ctx, client, controllerName, updated)
}
