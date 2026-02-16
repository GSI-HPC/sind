// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"strings"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// NodeShortNames returns the short hostname for each node defined in the config.
// Compute nodes are indexed sequentially across all compute groups, matching
// the indexing used in slurm.GenerateNodesConf.
func NodeShortNames(nodes []config.Node) []string {
	var names []string
	computeIdx := 0
	for _, n := range nodes {
		switch n.Role {
		case "controller", "submitter":
			names = append(names, n.Role)
		case "compute":
			count := n.Count
			if count <= 0 {
				count = 1
			}
			for i := 0; i < count; i++ {
				names = append(names, fmt.Sprintf("compute-%d", computeIdx))
				computeIdx++
			}
		}
	}
	return names
}

// PreflightCheck verifies that no Docker resources conflict with the cluster
// that would be created from the given configuration. It checks for existing
// networks, volumes, and containers with matching names.
func PreflightCheck(ctx context.Context, client *docker.Client, cfg *config.Cluster) error {
	var conflicts []string

	// Check cluster network.
	netName := NetworkName(cfg.Name)
	exists, err := client.NetworkExists(ctx, netName)
	if err != nil {
		return fmt.Errorf("checking network %s: %w", netName, err)
	}
	if exists {
		conflicts = append(conflicts, "network "+string(netName))
	}

	// Check cluster volumes.
	for _, vtype := range []string{"config", "munge", "data"} {
		volName := VolumeName(cfg.Name, vtype)
		exists, err := client.VolumeExists(ctx, volName)
		if err != nil {
			return fmt.Errorf("checking volume %s: %w", volName, err)
		}
		if exists {
			conflicts = append(conflicts, "volume "+string(volName))
		}
	}

	// Check node containers.
	for _, shortName := range NodeShortNames(cfg.Nodes) {
		containerName := ContainerName(cfg.Name, shortName)
		exists, err := client.ContainerExists(ctx, containerName)
		if err != nil {
			return fmt.Errorf("checking container %s: %w", containerName, err)
		}
		if exists {
			conflicts = append(conflicts, "container "+string(containerName))
		}
	}

	if len(conflicts) > 0 {
		return fmt.Errorf("conflicting resources already exist: %s", strings.Join(conflicts, ", "))
	}

	return nil
}
