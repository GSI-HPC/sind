// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"

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

// EnterTarget determines the target node for an interactive shell.
// Returns "submitter" if present in the cluster, otherwise "controller".
func EnterTarget(ctx context.Context, client *docker.Client, realm, clusterName string) (string, error) {
	entries, err := client.ListContainers(ctx,
		"label="+LabelRealm+"="+realm,
		"label="+LabelCluster+"="+clusterName)
	if err != nil {
		return "", fmt.Errorf("listing containers: %w", err)
	}

	for _, e := range entries {
		if e.Labels[LabelRole] == "submitter" {
			return "submitter", nil
		}
	}
	for _, e := range entries {
		if e.Labels[LabelRole] == "controller" {
			return "controller", nil
		}
	}
	return "", fmt.Errorf("no submitter or controller found in cluster %q", clusterName)
}

// BuildContainerExecArgs builds docker CLI arguments for running a command
// directly inside a cluster container via docker exec. The working directory
// is set to /data (the shared data mount). When command is nil, an interactive
// login shell is started.
func BuildContainerExecArgs(container docker.ContainerName, isTTY bool, command []string) []string {
	args := []string{"exec", "-i"}
	if isTTY {
		args = append(args, "-t")
	}
	args = append(args, "-w", "/data", string(container))
	if len(command) == 0 {
		args = append(args, "bash", "-l")
	} else {
		args = append(args, command...)
	}
	return args
}
