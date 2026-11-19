// SPDX-License-Identifier: LGPL-3.0-or-later

package nodecmd

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
)

func TestBuildSSHArgs(t *testing.T) {
	ssh := string(mesh.RealmNames(mesh.DefaultRealm).SSHContainer)
	tests := []struct {
		name          string
		node, cluster string
		tty           bool
		opts, command []string
		want          []string
	}{
		{"interactive", "worker-0", "dev", true, nil, nil, []string{"exec", "-i", "-t", ssh, "ssh", "--", "worker-0.dev.sind.sind"}},
		{"non-interactive", "worker-0", "dev", false, nil, nil, []string{"exec", "-i", ssh, "ssh", "--", "worker-0.dev.sind.sind"}},
		{"command", "controller", "default", false, nil, []string{"hostname"},
			[]string{"exec", "-i", ssh, "ssh", "--", "controller.default.sind.sind", "hostname"}},
		{"multi-word command", "worker-0", "dev", false, nil, []string{"ls", "-la", "/tmp"},
			[]string{"exec", "-i", ssh, "ssh", "--", "worker-0.dev.sind.sind", "ls", "-la", "/tmp"}},
		{"option", "worker-0", "dev", true, []string{"-v"}, nil, []string{"exec", "-i", "-t", ssh, "ssh", "-v", "--", "worker-0.dev.sind.sind"}},
		{"port forwarding", "controller", "default", true, []string{"-L", "8080:localhost:80"}, nil,
			[]string{"exec", "-i", "-t", ssh, "ssh", "-L", "8080:localhost:80", "--", "controller.default.sind.sind"}},
		{"force tty", "worker-0", "dev", true, []string{"-t"}, []string{"top"},
			[]string{"exec", "-i", "-t", ssh, "ssh", "-t", "--", "worker-0.dev.sind.sind", "top"}},
		{"command of a leading dash", "worker-0", "dev", false, nil, []string{"-oProxyCommand=id"},
			[]string{"exec", "-i", ssh, "ssh", "--", "worker-0.dev.sind.sind", "-oProxyCommand=id"}},
		{"options and command", "worker-0", "dev", false, []string{"-v", "-o", "StrictHostKeyChecking=no"}, []string{"uptime"},
			[]string{"exec", "-i", ssh, "ssh", "-v", "-o", "StrictHostKeyChecking=no", "--", "worker-0.dev.sind.sind", "uptime"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, BuildSSHArgs(SSHCommand{
				Realm: mesh.DefaultRealm, Cluster: tt.cluster, Node: tt.node,
				TTY: tt.tty, Options: tt.opts, Command: tt.command,
			}))
		})
	}
}

func TestBuildSSHArgs_RealmRelay(t *testing.T) {
	got := BuildSSHArgs(SSHCommand{Realm: "ci", Cluster: "dev", Node: "worker-0"})
	assert.Equal(t, []string{"exec", "-i", "ci-ssh", "ssh", "--", "worker-0.dev.ci.sind"}, got)
}

func TestBuildContainerExecArgs(t *testing.T) {
	tests := []struct {
		name            string
		container, user string
		workDir         string
		tty             bool
		command         []string
		want            []string
	}{
		{"interactive shell", "sind-dev-controller", "", cluster.DefaultDataMountPath, true, nil,
			[]string{"exec", "-i", "-t", "-w", "/data", "sind-dev-controller", "bash", "-l"}},
		{"non-interactive shell", "sind-dev-controller", "", cluster.DefaultDataMountPath, false, nil,
			[]string{"exec", "-i", "-w", "/data", "sind-dev-controller", "bash", "-l"}},
		{"command", "sind-dev-controller", "", cluster.DefaultDataMountPath, false, []string{"sinfo"},
			[]string{"exec", "-i", "-w", "/data", "sind-dev-controller", "sinfo"}},
		{"multi-word command", "sind-dev-worker-0", "", cluster.DefaultDataMountPath, false, []string{"srun", "hostname"},
			[]string{"exec", "-i", "-w", "/data", "sind-dev-worker-0", "srun", "hostname"}},
		{"custom work dir", "sind-dev-controller", "", "/shared", false, []string{"ls"},
			[]string{"exec", "-i", "-w", "/shared", "sind-dev-controller", "ls"}},
		// A cluster user starts in its home directory, not the data mount.
		{"user", "sind-dev-submitter", "alice", cluster.DefaultDataMountPath, true, nil,
			[]string{"exec", "-i", "-t", "-u", "alice", "-w", "/home/alice", "sind-dev-submitter", "bash", "-l"}},
		// root is the default user; its home is not under /home.
		{"root", "sind-dev-submitter", "root", cluster.DefaultDataMountPath, false, []string{"id"},
			[]string{"exec", "-i", "-w", "/data", "sind-dev-submitter", "id"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, BuildContainerExecArgs(docker.ContainerName(tt.container), tt.user, tt.workDir, tt.tty, tt.command))
		})
	}
}

// --- EnterTarget ---

func TestLogArgs(t *testing.T) {
	tests := []struct {
		name    string
		node    string
		service string // ServiceLogArgs when set, ContainerLogArgs otherwise
		opts    LogOptions
		want    []string
	}{
		{"container", "controller", "", LogOptions{}, []string{"logs", "sind-dev-controller"}},
		{"container follow", "worker-0", "", LogOptions{Follow: true}, []string{"logs", "--follow", "sind-dev-worker-0"}},
		{"container tail", "controller", "", LogOptions{Tail: new(20)}, []string{"logs", "--tail", "20", "sind-dev-controller"}},
		{"service", "controller", "slurmctld", LogOptions{}, []string{"exec", "sind-dev-controller", "journalctl", "-u", "slurmctld"}},
		{"service follow", "worker-0", "slurmd", LogOptions{Follow: true},
			[]string{"exec", "sind-dev-worker-0", "journalctl", "-u", "slurmd", "--follow"}},
		{"service tail", "controller", "slurmctld", LogOptions{Follow: true, Tail: new(0)},
			[]string{"exec", "sind-dev-controller", "journalctl", "-u", "slurmctld", "--follow", "-n", "0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ContainerLogArgs(mesh.DefaultRealm, "dev", tt.node, tt.opts)
			if tt.service != "" {
				got = ServiceLogArgs(mesh.DefaultRealm, "dev", tt.node, tt.service, tt.opts)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestServiceLogCommand(t *testing.T) {
	assert.Equal(t, []string{"journalctl", "--follow", "-n", "5"}, ServiceLogCommand("", LogOptions{Follow: true, Tail: new(5)}), "every unit")
	assert.Equal(t, []string{"journalctl", "-u", "slurmd"}, ServiceLogCommand("slurmd", LogOptions{}))
}
