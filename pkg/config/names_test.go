// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/testutil"
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

	// ssh is a valid realm, but no cluster name: its config volume would be
	// the realm's SSH volume.
	require.NoError(t, CheckName("realm", ReservedClusterName))
	require.EqualError(t, CheckName("cluster", ReservedClusterName),
		`invalid cluster name "ssh": it is reserved: the cluster's config volume, <realm>-ssh-config, would be the realm's SSH volume`)
}

func TestValidate_AccountingClusterName(t *testing.T) {
	// Slurm limits ClusterName to 40 characters with slurmdbd.
	name40 := strings.Repeat("a", MaxAccountingClusterName)
	name41 := name40 + "b"
	withDB := []Node{{Role: RoleController}, {Role: RoleDB}, {Role: RoleWorker}}
	for _, tt := range []struct {
		name    string
		nodes   []Node
		wantErr string
	}{
		{name40, withDB, ""},
		{name41, []Node{{Role: RoleController}, {Role: RoleWorker}}, ""},
		{name41, []Node{{Role: RoleController}, {Role: RoleDB, Managed: testutil.Ptr(false)}, {Role: RoleWorker}}, ""},
		{name41, withDB, `cluster name "` + name41 + `" has 41 characters: with a managed db node it may have at most 40, as slurmdbd builds its table names from it`},
	} {
		cfg := &Cluster{Kind: "Cluster", Name: tt.name, Nodes: tt.nodes}
		err := cfg.Validate()
		if tt.wantErr == "" {
			require.NoError(t, err)
			continue
		}
		require.EqualError(t, err, tt.wantErr)
	}
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
