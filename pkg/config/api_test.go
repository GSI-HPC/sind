// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"testing"

	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidate_APIAuthAltTypes(t *testing.T) {
	apiCluster := func(slurm Slurm, api Node) *Cluster {
		return &Cluster{
			Kind:  "Cluster",
			Name:  "default",
			Nodes: []Node{{Role: RoleController}, {Role: RoleDB}, api, {Role: RoleWorker}},
			Slurm: slurm,
		}
	}
	api := Node{Role: RoleAPI}

	tests := []struct {
		name    string
		slurm   Slurm
		wantErr string
	}{
		{"no AuthAltTypes", Slurm{}, ""},
		{"main lists auth/jwt", Slurm{Main: Section{Content: "AuthAltTypes=auth/jwt\n"}}, ""},
		{"main lists jwt without prefix", Slurm{Main: Section{Content: "AuthAltTypes=jwt\n"}}, ""},
		{"main sets AuthAltParameters only", Slurm{Main: Section{Content: "AuthAltParameters=jwks=/etc/slurm/jwks.json\n"}}, ""},
		{"slurmdbd lists auth/jwt", Slurm{Slurmdbd: Section{Content: "AuthAltTypes=auth/jwt\n"}}, ""},
		{
			"main lacks auth/jwt",
			Slurm{Main: Section{Content: "AuthAltTypes=auth/munge\n"}},
			"slurm main sets AuthAltTypes=auth/munge: the api node needs auth/jwt in it",
		},
		{
			"slurmdbd fragment lacks auth/jwt",
			Slurm{Slurmdbd: Section{Fragments: map[string]string{"auth": "AuthAltTypes=auth/none\n"}}},
			"slurm slurmdbd sets AuthAltTypes=auth/none: the api node needs auth/jwt in it",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := apiCluster(tt.slurm, api).Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.wantErr)
		})
	}

	t.Run("bare api node", func(t *testing.T) {
		bare := Node{Role: RoleAPI, Managed: testutil.Ptr(false)}
		assert.NoError(t, apiCluster(Slurm{Main: Section{Content: "AuthAltTypes=auth/munge\n"}}, bare).Validate())
	})
}
