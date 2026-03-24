// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import "github.com/GSI-HPC/sind/pkg/mesh"

// BuildSSHArgs builds the docker CLI arguments for running SSH through the
// sind-ssh relay container. The returned args are suitable for passing to
// docker directly (e.g. "docker exec -i -t sind-ssh ssh ...").
//
// node is the short name (e.g. "compute-0"), cluster is the cluster name,
// isTTY controls whether -t is added to docker exec, sshOptions are passed
// through to SSH before the target, and command is the optional remote command.
func BuildSSHArgs(node, cluster string, isTTY bool, sshOptions, command []string) []string {
	args := []string{"exec", "-i"}
	if isTTY {
		args = append(args, "-t")
	}
	args = append(args, string(mesh.SSHContainerName), "ssh")
	args = append(args, sshOptions...)
	args = append(args, DNSName(node, cluster))
	args = append(args, command...)
	return args
}
