// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import "strconv"

// LogOptions bounds and shapes the log output of ContainerLogArgs and
// ServiceLogArgs.
type LogOptions struct {
	// Follow streams new output until interrupted.
	Follow bool
	// Tail limits the output to the last Tail lines; nil shows everything.
	Tail *int
}

// ContainerLogArgs builds docker CLI arguments for streaming container logs.
// node is the short name (e.g. "controller", "worker-0"), cluster is the
// cluster name, and opts selects following and the number of lines.
func ContainerLogArgs(realm, node, cluster string, opts LogOptions) []string {
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
// node is the short name, cluster is the cluster name, service is the systemd
// unit name (e.g. "slurmctld", "slurmd"), and opts selects following and the
// number of lines.
func ServiceLogArgs(realm, node, cluster, service string, opts LogOptions) []string {
	args := []string{"exec", string(ContainerName(realm, cluster, node)),
		"journalctl", "-u", service}
	if opts.Follow {
		args = append(args, "--follow")
	}
	if opts.Tail != nil {
		args = append(args, "-n", strconv.Itoa(*opts.Tail))
	}
	return args
}
