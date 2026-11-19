// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
			assert.Equal(t, []string{"inspect", "sind-dev-node", "--type", "container"}, m.Calls[0].Args)
		})
	}
}

func TestNodeServicesOfContainer_OtherCluster(t *testing.T) {
	var m mock.Executor
	m.AddResult(`[{"Id":"x","Name":"/ci-42-dev-node","State":{"Status":"running"},"Config":{"Labels":{"sind.realm":"ci-42","sind.cluster":"dev","sind.role":"worker"}}}]`, "", nil)

	_, err := NodeServices(t.Context(), docker.NewClient(&m), "ci", "42-dev", "node")

	require.EqualError(t, err, `node "node" not found in cluster "42-dev"`)
	assert.ErrorIs(t, err, ErrNodeNotFound)
}

func TestNodeServicesOfContainer_Helper(t *testing.T) {
	var m mock.Executor
	m.AddResult(`[{"Id":"x","Name":"/sind-dev-config-helper","State":{"Status":"created"},"Config":{"Labels":{"sind.realm":"sind","sind.cluster":"dev"}}}]`, "", nil)

	_, err := NodeServices(t.Context(), docker.NewClient(&m), "sind", "dev", "config-helper")

	require.EqualError(t, err, `node "config-helper" not found in cluster "dev"`)
	assert.ErrorIs(t, err, ErrNodeNotFound)
}

// TestNodeServicesOfContainer_Missing checks that a node without a
// container is a missing node, as for the other functions that take nodes.
func TestNodeServicesOfContainer_Missing(t *testing.T) {
	var m mock.Executor
	m.AddResult("", testutil.NoSuchContainer("sind-dev-node"), testutil.ExitCode1(t))

	_, err := NodeServices(t.Context(), docker.NewClient(&m), "sind", "dev", "node")

	require.EqualError(t, err, `node "node" not found in cluster "dev"`)
	assert.ErrorIs(t, err, ErrNodeNotFound)
	assert.True(t, docker.IsNotFound(err), "docker's error stays wrapped")
}

func TestNodeServicesOfContainer_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Cannot connect to the Docker daemon\n", testutil.ExitCode1(t))

	_, err := NodeServices(t.Context(), docker.NewClient(&m), "sind", "dev", "node")

	require.ErrorContains(t, err, "inspecting container: ")
	assert.NotErrorIs(t, err, ErrNodeNotFound)
}
