// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"testing"

	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidate_SlurmdbdParameters(t *testing.T) {
	dbCluster := func(identity IdentityMode, slurmdbd Section) *Cluster {
		return &Cluster{
			Kind:     "Cluster",
			Name:     "default",
			Identity: Identity{Mode: identity},
			Nodes:    []Node{{Role: RoleController}, {Role: RoleDB}, {Role: RoleSubmitter}, {Role: RoleWorker}},
			Slurm:    Slurm{Slurmdbd: slurmdbd},
		}
	}

	tests := []struct {
		name     string
		identity IdentityMode
		slurmdbd Section
		wantErr  string
	}{
		{name: "none"},
		{name: "StoragePass and archiving", slurmdbd: Section{Content: "StoragePass=secret\nArchiveEvents=yes\n"}},
		{name: "StorageUser", slurmdbd: Section{Content: "StorageUser=slurmdbd\n"},
			wantErr: "slurm slurmdbd must not set StorageUser: sind sets up the accounting database on the db node and points slurmdbd at it"},
		{name: "StorageLoc in a fragment", slurmdbd: Section{Fragments: map[string]string{"site": "storageloc=acct\n"}},
			wantErr: "slurm slurmdbd must not set StorageLoc"},
		{name: "StorageHost", slurmdbd: Section{Content: "StorageHost=dbhost.example.org\n"}, wantErr: "must not set StorageHost"},
		{name: "StorageType", slurmdbd: Section{Content: "StorageType=accounting_storage/mysql\n"}, wantErr: "must not set StorageType"},
		{name: "DbdHost", slurmdbd: Section{Content: "DbdHost=slurmdbd01\n"}, wantErr: "must not set DbdHost"},
		{name: "SlurmUser", slurmdbd: Section{Content: "SlurmUser=slurmdbd\n"}, wantErr: "must not set SlurmUser"},
		{name: "munge AuthType", slurmdbd: Section{Content: "AuthType=auth/munge\n"}},
		{name: "munge AuthType without prefix", slurmdbd: Section{Content: "AuthType=munge\n"}},
		{name: "auth/slurm without clientIds", slurmdbd: Section{Content: "AuthType=auth/slurm\n"},
			wantErr: "slurm slurmdbd sets AuthType=auth/slurm: the cluster authenticates with auth/munge"},
		{name: "clientIds with auth/slurm", identity: IdentityClientIDs, slurmdbd: Section{Content: "AuthType=auth/slurm\nAuthInfo=use_client_ids,cred_expire=60\n"}},
		{name: "clientIds with a site's munge", identity: IdentityClientIDs, slurmdbd: Section{Content: "AuthType=auth/munge\n"},
			wantErr: "slurm slurmdbd sets AuthType=auth/munge: the cluster authenticates with auth/slurm"},
		{name: "clientIds without use_client_ids", identity: IdentityClientIDs, slurmdbd: Section{Content: "AuthInfo=cred_expire=60\n"},
			wantErr: "slurm slurmdbd sets AuthInfo=cred_expire=60: identity clientIds needs use_client_ids in it"},
		{name: "AuthInfo without clientIds", slurmdbd: Section{Content: "AuthInfo=socket=/run/munge/munge.socket.2\n"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := dbCluster(tt.identity, tt.slurmdbd).Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}

	// Only a managed db node runs sind's accounting setup; the slurmdbd
	// section then is an error of its own.
	unmanaged := dbCluster("", Section{Content: "StorageUser=slurmdbd\n"})
	unmanaged.Nodes[1].Managed = testutil.Ptr(false)
	require.EqualError(t, unmanaged.Validate(), "slurm slurmdbd requires a managed db node: sind writes no slurmdbd.conf for an unmanaged one")
}
