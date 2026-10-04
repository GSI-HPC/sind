// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"testing"

	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_Identity(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Identity
	}{
		{"omitted", "kind: Cluster", Identity{}},
		{"mode", "kind: Cluster\nidentity: nssSlurm", Identity{Mode: IdentityNSSSlurm}},
		{"object", "kind: Cluster\nidentity:\n  mode: clientIds\n  controllerUsers: true", Identity{Mode: IdentityClientIDs, ControllerUsers: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tt.input))
			require.NoError(t, err)
			assert.Equal(t, tt.want, cfg.Identity)
		})
	}
}

func TestParse_IdentityErrors(t *testing.T) {
	for _, tt := range []struct {
		name    string
		input   string
		wantErr string
	}{
		{"unknown field", "kind: Cluster\nidentity:\n  mode: clientIds\n  auth: slurm", `identity must be a mode or an object with mode and controllerUsers: json: unknown field "auth"`},
		{"list", "kind: Cluster\nidentity: [nssSlurm]", "identity must be a mode or an object with mode and controllerUsers"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.input))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestApplyDefaults_Identity(t *testing.T) {
	cfg := &Cluster{Kind: "Cluster", Name: DefaultClusterName}
	cfg.ApplyDefaults()
	assert.Equal(t, IdentityLocal, cfg.Identity.Mode)

	cfg = &Cluster{Kind: "Cluster", Name: DefaultClusterName, Identity: Identity{Mode: IdentityClientIDs}}
	cfg.ApplyDefaults()
	assert.Equal(t, IdentityClientIDs, cfg.Identity.Mode)
}

func TestIdentity_UsesNSSSlurm(t *testing.T) {
	assert.False(t, Identity{}.UsesNSSSlurm())
	assert.False(t, Identity{Mode: IdentityLocal}.UsesNSSSlurm())
	assert.True(t, Identity{Mode: IdentityNSSSlurm}.UsesNSSSlurm())
	assert.True(t, Identity{Mode: IdentityClientIDs}.UsesNSSSlurm())
}

func TestValidate_IdentityParameters(t *testing.T) {
	// A parameter of the mode that slurm.main sets must keep sind's value:
	// sind writes none of its own then.
	for _, tt := range []struct {
		mode    IdentityMode
		main    string
		wantErr string
	}{
		{IdentityLocal, "LaunchParameters=use_interactive_step\nAuthType=auth/munge\n", ""},
		{IdentityNSSSlurm, "LaunchParameters=use_interactive_step,enable_nss_slurm\n", ""},
		{IdentityNSSSlurm, "AuthType=auth/munge\n", ""},
		{IdentityNSSSlurm, "LaunchParameters=use_interactive_step\n",
			"slurm main sets LaunchParameters=use_interactive_step: identity nssSlurm needs enable_nss_slurm in it"},
		{IdentityNSSSlurm, "LaunchParameters=enable_nss_slurm\nlaunchparameters = use_interactive_step # later wins\n",
			"slurm main sets LaunchParameters=use_interactive_step: identity nssSlurm needs enable_nss_slurm in it"},
		{IdentityClientIDs, "AuthType=auth/slurm\nCredType=cred/slurm\nAuthInfo=use_client_ids,cred_expire=30\n", ""},
		{IdentityClientIDs, "AuthType=auth/munge\n",
			"slurm main sets AuthType=auth/munge: identity clientIds needs auth/slurm in it"},
		{IdentityClientIDs, "CredType=cred/munge\n",
			"slurm main sets CredType=cred/munge: identity clientIds needs cred/slurm in it"},
		{IdentityClientIDs, "AuthInfo=cred_expire=30\n",
			"slurm main sets AuthInfo=cred_expire=30: identity clientIds needs use_client_ids in it"},
		{IdentityClientIDs, "LaunchParameters=\"\"\n",
			"slurm main sets LaunchParameters=: identity clientIds needs enable_nss_slurm in it"},
	} {
		cfg := &Cluster{Kind: "Cluster", Name: DefaultClusterName, Identity: Identity{Mode: tt.mode}, Slurm: Slurm{Main: Section{Content: tt.main}}}
		cfg.ApplyDefaults()
		err := cfg.Validate()
		if tt.wantErr == "" {
			assert.NoError(t, err, tt.main)
			continue
		}
		assert.EqualError(t, err, tt.wantErr, tt.main)
	}
}

func TestIdentityMode_SlurmParameters(t *testing.T) {
	assert.Nil(t, IdentityLocal.SlurmParameters())
	assert.Equal(t, []SlurmParameter{{"LaunchParameters", "enable_nss_slurm"}}, IdentityNSSSlurm.SlurmParameters())
	assert.Equal(t, []SlurmParameter{
		{"AuthType", "auth/slurm"},
		{"CredType", "cred/slurm"},
		{"AuthInfo", "use_client_ids"},
		{"LaunchParameters", "enable_nss_slurm"},
	}, IdentityClientIDs.SlurmParameters())
}

func TestValidate_Identity(t *testing.T) {
	unmanaged := []Node{{Role: RoleController, Managed: testutil.Ptr(false)}, {Role: RoleWorker}}
	tests := []struct {
		name     string
		identity Identity
		nodes    []Node
		wantErr  string
	}{
		{name: "empty", identity: Identity{}},
		{name: "local", identity: Identity{Mode: IdentityLocal}},
		{name: "nssSlurm", identity: Identity{Mode: IdentityNSSSlurm}},
		{name: "clientIds", identity: Identity{Mode: IdentityClientIDs}},
		{name: "clientIds with controllerUsers", identity: Identity{Mode: IdentityClientIDs, ControllerUsers: true}},
		{name: "local on an unmanaged cluster", identity: Identity{Mode: IdentityLocal}, nodes: unmanaged},
		{name: "unknown mode", identity: Identity{Mode: "sssd"}, wantErr: `identity must be "local", "nssSlurm" or "clientIds", got "sssd"`},
		{name: "wrong case", identity: Identity{Mode: "nss_slurm"}, wantErr: `got "nss_slurm"`},
		{name: "controllerUsers with nssSlurm", identity: Identity{Mode: IdentityNSSSlurm, ControllerUsers: true}, wantErr: `identity controllerUsers is only valid with mode "clientIds"`},
		{name: "controllerUsers with local", identity: Identity{ControllerUsers: true}, wantErr: `identity controllerUsers is only valid with mode "clientIds"`},
		{name: "nssSlurm on an unmanaged cluster", identity: Identity{Mode: IdentityNSSSlurm}, nodes: unmanaged, wantErr: "identity nssSlurm requires a managed controller: sind writes no Slurm configuration for an unmanaged cluster"},
		{name: "clientIds on an unmanaged cluster", identity: Identity{Mode: IdentityClientIDs}, nodes: unmanaged, wantErr: "identity clientIds requires a managed controller"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Cluster{Kind: "Cluster", Name: DefaultClusterName, Identity: tt.identity, Nodes: tt.nodes}
			cfg.ApplyDefaults()
			err := cfg.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
