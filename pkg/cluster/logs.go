// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// NodeServices returns the systemd units of a node whose journal
// `sind logs` shows, by name: those get node checks on it, which depend
// on its role and on whether sind manages Slurm on it. It inspects the
// node's container, for shell completion; a container whose labels name
// another realm or cluster (see ownedBy), or a helper (see isNode), is not
// the node. A node without such a container fails with ErrNodeNotFound,
// also when the cluster does not exist; for a node without a container the
// error wraps docker's too (docker.IsNotFound).
func NodeServices(ctx context.Context, client *docker.Client, realm, clusterName, shortName string) ([]string, error) {
	info, err := client.InspectContainer(ctx, ContainerName(realm, clusterName, shortName))
	if err != nil && !docker.IsNotFound(err) {
		return nil, fmt.Errorf("inspecting container: %w", err)
	}
	if err != nil {
		return nil, errorWithCause(ErrNodeNotFound, err, "node %q not found in cluster %q", shortName, clusterName)
	}
	if !ownedBy(info.Labels, realm, clusterName) || !isNode(info.Labels) {
		return nil, errorWith(ErrNodeNotFound, "node %q not found in cluster %q", shortName, clusterName)
	}
	services := nodeServices(config.Role(info.Labels[LabelRole]), info.Labels)
	names := make([]string, len(services))
	for i, s := range services {
		names[i] = string(s)
	}
	return names, nil
}
