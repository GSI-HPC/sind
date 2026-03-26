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
func BuildSSHArgs(sshContainer docker.ContainerName, node, cluster string, isTTY bool, sshOptions, command []string) []string {
	args := []string{"exec", "-i"}
	if isTTY {
		args = append(args, "-t")
	}
	args = append(args, string(sshContainer), "ssh")
	args = append(args, sshOptions...)
	args = append(args, DNSName(node, cluster))
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
		return "", fmt.Errorf("listing cluster containers: %w", err)
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

// ExecArgs builds docker CLI arguments for a one-shot command execution on
// the cluster's submitter (or controller). The returned args are suitable for
// passing to docker directly.
func ExecArgs(ctx context.Context, client *docker.Client, realm string, sshContainer docker.ContainerName, clusterName string, command []string) ([]string, error) {
	target, err := EnterTarget(ctx, client, realm, clusterName)
	if err != nil {
		return nil, err
	}
	return BuildSSHArgs(sshContainer, target, clusterName, false, nil, command), nil
}
