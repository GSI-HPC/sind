// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

// ContainerLogArgs builds docker CLI arguments for streaming container logs.
// node is the short name (e.g. "controller", "worker-0"), cluster is the
// cluster name, and follow controls whether --follow is added.
func ContainerLogArgs(node, cluster string, follow bool) []string {
	args := []string{"logs"}
	if follow {
		args = append(args, "--follow")
	}
	args = append(args, string(ContainerName(cluster, node)))
	return args
}

// ServiceLogArgs builds docker CLI arguments for streaming service journal logs.
// node is the short name, cluster is the cluster name, service is the systemd
// unit name (e.g. "slurmctld", "slurmd"), and follow controls whether
// --follow is added.
func ServiceLogArgs(node, cluster, service string, follow bool) []string {
	args := []string{"exec", string(ContainerName(cluster, node)),
		"journalctl", "-u", service}
	if follow {
		args = append(args, "--follow")
	}
	return args
}
