// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"testing"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveCluster_Precedence(t *testing.T) {
	tests := []struct {
		name   string
		env    string
		config string
		want   string
	}{
		{"env over config", "from-env", "from-config", "from-env"},
		{"config over default", "", "from-config", "from-config"},
		{"env over default", "from-env", "", "from-env"},
		{"default", "", "", config.DefaultClusterName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SIND_CLUSTER", tt.env)
			got, err := ResolveCluster(tt.config)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveCluster_InvalidEnv(t *testing.T) {
	t.Setenv("SIND_CLUSTER", "Dev")

	_, err := ResolveCluster("from-config")

	require.EqualError(t, err, `SIND_CLUSTER: invalid cluster name "Dev": it has uppercase letters`)
}
