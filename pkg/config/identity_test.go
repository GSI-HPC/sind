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
