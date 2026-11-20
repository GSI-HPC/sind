// SPDX-License-Identifier: LGPL-3.0-or-later

// Package nodecmd builds the docker command lines of sind ssh, enter, exec
// and logs, and of the log views of sind ui, which run in the terminal or
// stream a node's output.
package nodecmd

import (
	"strconv"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
)

// SSHCommand is an ssh session on a node, run by BuildSSHArgs.
type SSHCommand struct {
	// Realm, Cluster and Node name the node; Node is its short name
	// (e.g. "worker-0").
	Realm, Cluster, Node string
	// TTY adds -t to docker exec.
	TTY bool
	// Options are passed to ssh before the target.
	Options []string
	// Command is the remote command, if any.
	Command []string
}

// BuildSSHArgs builds the docker CLI arguments for running ssh through the
// SSH relay container of the realm. The returned args are suitable for
// passing to docker directly (e.g. "docker exec -i -t sind-ssh ssh ...").
// A "--" ends ssh's options before the target, as ssh reads a word after the
// target that starts with "-" as an option, so the command may start with one.
func BuildSSHArgs(c SSHCommand) []string {
	args := []string{"exec", "-i"}
	if c.TTY {
		args = append(args, "-t")
	}
	args = append(args, string(mesh.RealmNames(c.Realm).SSHContainer), "ssh")
	args = append(args, c.Options...)
	args = append(args, "--", cluster.DNSName(c.Node, c.Cluster, c.Realm))
	args = append(args, c.Command...)
	return args
}

// BuildContainerExecArgs builds docker CLI arguments for running a command
// directly inside a cluster container via docker exec. The command runs as root
// in workDir, the node's data mount point (see cluster.EnterTarget), or, when
// user names a cluster user, as that user in its home directory. When command
// is nil, an interactive login shell is started.
func BuildContainerExecArgs(container docker.ContainerName, user, workDir string, isTTY bool, command []string) []string {
	args := []string{"exec", "-i"}
	if isTTY {
		args = append(args, "-t")
	}
	if user != "" && user != "root" {
		args = append(args, "-u", user)
		workDir = cluster.HomeDir(user)
	}
	args = append(args, "-w", workDir, string(container))
	if len(command) == 0 {
		args = append(args, "bash", "-l")
	} else {
		args = append(args, command...)
	}
	return args
}

// LogOptions bounds and shapes the log output of ContainerLogArgs and
// ServiceLogArgs.
type LogOptions struct {
	// Follow streams new output until interrupted.
	Follow bool
	// Tail limits the output to the last Tail lines; nil shows everything.
	Tail *int
}

// ContainerLogArgs builds docker CLI arguments for streaming container logs.
// clusterName is the cluster name, node the short name (e.g. "controller",
// "worker-0"), and opts selects following and the number of lines.
func ContainerLogArgs(realm, clusterName, node string, opts LogOptions) []string {
	args := []string{"logs"}
	if opts.Follow {
		args = append(args, "--follow")
	}
	if opts.Tail != nil {
		args = append(args, "--tail", strconv.Itoa(*opts.Tail))
	}
	args = append(args, string(cluster.ContainerName(realm, clusterName, node)))
	return args
}

// ServiceLogArgs builds docker CLI arguments for streaming service journal logs.
// clusterName is the cluster name, node the short name, service the systemd unit
// name (e.g. "slurmctld", "slurmd"), and opts selects following and the
// number of lines.
func ServiceLogArgs(realm, clusterName, node, service string, opts LogOptions) []string {
	return append([]string{"exec", string(cluster.ContainerName(realm, clusterName, node))}, ServiceLogCommand(service, opts)...)
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
