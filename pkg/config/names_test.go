// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckName(t *testing.T) {
	require.NoError(t, CheckName("cluster", DefaultClusterName))
	require.NoError(t, CheckName("realm", "sind"))
	require.NoError(t, CheckName("realm", "ci-42"))
	require.EqualError(t, CheckName("cluster", "Dev"), `invalid cluster name "Dev": it has uppercase letters`)

	err := CheckName("realm", "../x")
	require.EqualError(t, err, `invalid realm name "../x": '.' is not a letter, a digit or a hyphen`)
	err = CheckName("cluster", "")
	require.EqualError(t, err, `invalid cluster name "": it is empty`)
}

func TestValidate_Names(t *testing.T) {
	nodes := []Node{{Role: RoleController}, {Role: RoleWorker}}
	tests := []struct {
		name, cluster, realm string
		wantErr              string
	}{
		{"defaults", DefaultClusterName, "", ""},
		{"custom realm", "dev", "ci-42", ""},
		{"cluster with underscore", "my_cluster", "", `invalid cluster name "my_cluster"`},
		{"cluster with dot", "dev.test", "", `invalid cluster name "dev.test"`},
		{"cluster with uppercase", "Dev", "", `invalid cluster name "Dev": it has uppercase letters`},
		{"realm with uppercase", "dev", "CI-42", `invalid realm name "CI-42": it has uppercase letters`},
		{"cluster too long", "a123456789012345678901234567890123456789012345678901234567890123", "", `invalid cluster name`},
		{"realm escaping the state dir", "dev", "../x", `invalid realm name "../x"`},
		{"realm with slash", "dev", "a/b", `invalid realm name "a/b"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Cluster{Kind: "Cluster", Name: tt.cluster, Realm: tt.realm, Nodes: nodes}
			err := cfg.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
