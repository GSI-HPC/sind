// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestNodeServicesOfContainer(t *testing.T) {
	tests := []struct {
		name   string
		labels string
		want   []string
	}{
		{"controller", `{"sind.role":"controller"}`, []string{"munge", "sshd", "slurmctld"}},
		{"worker", `{"sind.role":"worker"}`, []string{"munge", "sshd", "slurmd"}},
		{"db", `{"sind.role":"db"}`, []string{"munge", "sshd", "mariadb", "slurmdbd"}},
		{"unmanaged worker", `{"sind.role":"worker","sind.managed":"false"}`, []string{"munge", "sshd"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			m.AddResult(`[{"Id":"x","Name":"/sind-dev-node","State":{"Status":"running"},"Config":{"Labels":`+tt.labels+`}}]`, "", nil)

			names, err := NodeServices(t.Context(), docker.NewClient(&m), "sind", "dev", "node")

			require.NoError(t, err)
			assert.Equal(t, tt.want, names)
			require.Len(t, m.Calls, 1)
			assert.Equal(t, []string{"inspect", "sind-dev-node"}, m.Calls[0].Args)
		})
	}
}

func TestNodeServicesOfContainer_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", testutil.NoSuchContainer("sind-dev-node"), testutil.ExitCode1(t))

	_, err := NodeServices(t.Context(), docker.NewClient(&m), "sind", "dev", "node")

	require.ErrorContains(t, err, "inspecting container: ")
	assert.True(t, docker.IsNotFound(err))
}
