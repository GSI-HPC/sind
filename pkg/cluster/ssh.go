// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// BuildSSHArgs builds the docker CLI arguments for running SSH through the
// sind-ssh relay container. The returned args are suitable for passing to
// docker directly (e.g. "docker exec -i -t sind-ssh ssh ...").
//
// node is the short name (e.g. "worker-0"), cluster is the cluster name,
// isTTY controls whether -t is added to docker exec, sshOptions are passed
// through to SSH before the target, and command is the optional remote command.
func BuildSSHArgs(sshContainer docker.ContainerName, node, cluster, realm string, isTTY bool, sshOptions, command []string) []string {
	args := []string{"exec", "-i"}
	if isTTY {
		args = append(args, "-t")
	}
	args = append(args, string(sshContainer), "ssh")
	args = append(args, sshOptions...)
	args = append(args, DNSName(node, cluster, realm))
	args = append(args, command...)
	return args
}

// EnterTarget determines the target node for sind enter and sind exec, and
// the directory they start in: the node's data mount point.
// The target is "submitter" if present in the cluster, otherwise the
// controller: in a primary/backup pair the one in control, falling back to
// "controller" when that cannot be determined. In an unmanaged cluster sind
// does not know which controller is in control, so the running one is
// picked, preferring "controller".
func EnterTarget(ctx context.Context, client *docker.Client, realm, clusterName string) (target, workDir string, err error) {
	entries, err := client.ListContainers(ctx,
		"label="+LabelRealm+"="+realm,
		"label="+LabelCluster+"="+clusterName)
	if err != nil {
		return "", "", fmt.Errorf("listing containers: %w", err)
	}

	target, node, err := enterTarget(ctx, client, realm, clusterName, entries)
	if err != nil {
		return "", "", err
	}
	return target, DataMountPath(node.Labels), nil
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
	if !IsManaged(controller.Labels) {
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

// BuildContainerExecArgs builds docker CLI arguments for running a command
// directly inside a cluster container via docker exec. The working directory
// is workDir, the node's data mount point (see EnterTarget). When command is
// nil, an interactive login shell is started.
func BuildContainerExecArgs(container docker.ContainerName, workDir string, isTTY bool, command []string) []string {
	args := []string{"exec", "-i"}
	if isTTY {
		args = append(args, "-t")
	}
	args = append(args, "-w", workDir, string(container))
	if len(command) == 0 {
		args = append(args, "bash", "-l")
	} else {
		args = append(args, command...)
	}
	return args
}
