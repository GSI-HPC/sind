// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"testing"

	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
)

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
			got := ContainerLogArgs(mesh.DefaultRealm, tt.node, "dev", tt.opts)
			if tt.service != "" {
				got = ServiceLogArgs(mesh.DefaultRealm, tt.node, "dev", tt.service, tt.opts)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}
