// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// EnterTarget determines the target node for sind enter and sind exec, and
// the directory they start in: the node's data mount point.
// The target is "submitter" if present in the cluster, otherwise the
// controller: in a primary/backup pair the one in control, falling back to
// "controller" when that cannot be determined. In an unmanaged cluster sind
// does not know which controller is in control, so the running one is
// picked, preferring "controller".
func EnterTarget(ctx context.Context, client *docker.Client, realm, clusterName string) (target, workDir string, err error) {
	entries, err := existingClusterContainers(ctx, client, realm, clusterName)
	if err != nil {
		return "", "", err
	}

	target, node, err := enterTarget(ctx, client, realm, clusterName, entries)
	if err != nil {
		return "", "", err
	}
	return target, dataMountPath(node.Labels), nil
}

// enterTarget picks the target of EnterTarget from the cluster's containers
// and returns its short name and container.
func enterTarget(ctx context.Context, client *docker.Client, realm, clusterName string, entries []docker.ContainerListEntry) (string, docker.ContainerListEntry, error) {
	for _, e := range entries {
		if config.Role(e.Labels[LabelRole]) == config.RoleSubmitter {
			return string(config.RoleSubmitter), e, nil
		}
	}
	controller, ok := findController(entries, realm, clusterName)
	if !ok {
		return "", docker.ContainerListEntry{}, fmt.Errorf("no submitter or controller found in cluster %q", clusterName)
	}
	primary := ContainerName(realm, clusterName, string(config.RoleController))
	if controller.Name != primary {
		// Only the backup runs.
		return ControllerBackupShortName, controller, nil
	}
	if !isManaged(controller.Labels) {
		return string(config.RoleController), controller, nil
	}
	// Both controllers run: the heartbeat tells whether the backup has
	// taken over (e.g. after scontrol takeover, which leaves the primary
	// container running without slurmctld).
	for _, e := range entries {
		if e.Name == ContainerName(realm, clusterName, ControllerBackupShortName) && e.State == docker.StateRunning {
			if hb, ok := readHeartbeat(ctx, client, controller.Name); ok && hb.inControl(1, true) {
				return ControllerBackupShortName, e, nil
			}
		}
	}
	return string(config.RoleController), controller, nil
}
