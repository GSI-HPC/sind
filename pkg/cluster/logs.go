// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"strconv"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// LogOptions bounds and shapes the log output of ContainerLogArgs and
// ServiceLogArgs.
type LogOptions struct {
	// Follow streams new output until interrupted.
	Follow bool
	// Tail limits the output to the last Tail lines; nil shows everything.
	Tail *int
}

// ContainerLogArgs builds docker CLI arguments for streaming container logs.
// cluster is the cluster name, node the short name (e.g. "controller",
// "worker-0"), and opts selects following and the number of lines.
func ContainerLogArgs(realm, cluster, node string, opts LogOptions) []string {
	args := []string{"logs"}
	if opts.Follow {
		args = append(args, "--follow")
	}
	if opts.Tail != nil {
		args = append(args, "--tail", strconv.Itoa(*opts.Tail))
	}
	args = append(args, string(ContainerName(realm, cluster, node)))
	return args
}

// ServiceLogArgs builds docker CLI arguments for streaming service journal logs.
// cluster is the cluster name, node the short name, service the systemd unit
// name (e.g. "slurmctld", "slurmd"), and opts selects following and the
// number of lines.
func ServiceLogArgs(realm, cluster, node, service string, opts LogOptions) []string {
	return append([]string{"exec", string(ContainerName(realm, cluster, node))}, ServiceLogCommand(service, opts)...)
}

// ServiceLogCommand returns the journalctl command line of ServiceLogArgs,
// which runs in the node: the journal of the unit service, all units for
// an empty one, with opts.
func ServiceLogCommand(service string, opts LogOptions) []string {
	args := []string{"journalctl"}
	if service != "" {
		args = append(args, "-u", service)
	}
	if opts.Follow {
		args = append(args, "--follow")
	}
	if opts.Tail != nil {
		args = append(args, "-n", strconv.Itoa(*opts.Tail))
	}
	return args
}

// NodeServices returns the systemd units of a node whose journal
// ServiceLogArgs shows, by name: those get node checks on it, which depend
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
