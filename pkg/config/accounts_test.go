// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_Accounts(t *testing.T) {
	input := `kind: Cluster
accounts:
  - physics
  - name: theory
    parent: physics
    limits:
      GrpTRES: cpu=4
      MaxJobs: 1
      Fairshare: 1.5
users:
  - name: alice
    accounts: [theory, physics]
    coordinator: [physics]
    adminLevel: operator`

	cfg, err := Parse([]byte(input))
	require.NoError(t, err)
	assert.Equal(t, []Account{
		{Name: "physics"},
		{Name: "theory", Parent: "physics", Limits: Limits{"GrpTRES": "cpu=4", "MaxJobs": "1", "Fairshare": "1.5"}},
	}, cfg.Accounts)
	assert.Equal(t, []User{{Name: "alice", Accounts: []string{"theory", "physics"}, Coordinator: []string{"physics"}, AdminLevel: AdminOperator}}, cfg.Users)
}

func TestParse_AccountsErrors(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name:    "unknown account field",
			input:   "kind: Cluster\naccounts:\n  - name: physics\n    qos: normal",
			wantErr: `account must be a name or an object: json: unknown field "qos"`,
		},
		{
			name:    "account is a list",
			input:   "kind: Cluster\naccounts:\n  - [physics]",
			wantErr: "account must be a name or an object",
		},
		{
			name:    "limits is a list",
			input:   "kind: Cluster\naccounts:\n  - name: physics\n    limits: [MaxJobs=1]",
			wantErr: "limits must be a map of option names to values",
		},
		{
			name:    "limit is a bool",
			input:   "kind: Cluster\naccounts:\n  - name: physics\n    limits:\n      MaxJobs: true",
			wantErr: "limit MaxJobs must be a string or a number, got true",
		},
		{
			name:    "limit is a map",
			input:   "kind: Cluster\naccounts:\n  - name: physics\n    limits:\n      GrpTRES: {cpu: 4}",
			wantErr: `limit GrpTRES must be a string or a number, got {"cpu":4}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.input))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestLimits_Keys(t *testing.T) {
	assert.Equal(t, []string{"GrpTRES", "MaxJobs", "fairshare"}, Limits{"MaxJobs": "1", "fairshare": "2", "GrpTRES": "cpu=4"}.Keys())
	assert.Empty(t, Limits(nil).Keys())
}

func TestUsesAccounts(t *testing.T) {
	for _, tt := range []struct {
		name string
		cfg  Cluster
		want bool
	}{
		{"none", Cluster{Users: []User{{Name: "alice"}}}, false},
		{"accounts", Cluster{Accounts: []Account{{Name: "physics"}}}, true},
		{"user accounts", Cluster{Users: []User{{Name: "alice"}, {Name: "bob", Accounts: []string{"physics"}}}}, true},
		{"coordinator", Cluster{Users: []User{{Name: "alice", Coordinator: []string{"physics"}}}}, true},
		{"admin level", Cluster{Users: []User{{Name: "alice", AdminLevel: AdminAdmin}}}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.cfg.UsesAccounts())
		})
	}
}

func TestValidate_Accounts(t *testing.T) {
	withDB := []Node{{Role: RoleController}, {Role: RoleDB}, {Role: RoleWorker}}
	tests := []struct {
		name     string
		nodes    []Node
		accounts []Account
		users    []User
		wantErr  string
	}{
		{name: "valid", accounts: []Account{
			{Name: "physics", Parent: "root", Limits: Limits{"GrpTRES": "cpu=4", "Description": "Physics"}},
			{Name: "theory", Parent: "physics"},
			{Name: "0_ops-" + strings.Repeat("x", 58)},
		}, users: []User{
			{Name: "alice", Accounts: []string{"theory", "physics"}, Coordinator: []string{"physics"}, AdminLevel: AdminAdmin},
			{Name: "bob", Accounts: []string{"physics"}, AdminLevel: AdminOperator},
			{Name: "dave"},
		}},
		{name: "no db node", nodes: []Node{{Role: RoleController}, {Role: RoleWorker}}, accounts: []Account{{Name: "physics"}},
			wantErr: "accounts, and the users' accounts, coordinator and adminLevel, require a managed db node: sind creates Slurm accounts with sacctmgr, which needs slurmdbd"},
		{name: "unmanaged db node", nodes: []Node{{Role: RoleController}, {Role: RoleDB, Managed: testutil.Ptr(false)}, {Role: RoleWorker}}, users: []User{{Name: "alice", AdminLevel: AdminAdmin}},
			wantErr: "require a managed db node"},
		{name: "unmanaged cluster", nodes: []Node{{Role: RoleController, Managed: testutil.Ptr(false)}, {Role: RoleDB}, {Role: RoleWorker}}, accounts: []Account{{Name: "physics"}},
			wantErr: "require a managed db node"},
		{name: "uppercase name", accounts: []Account{{Name: "Physics"}},
			wantErr: `invalid account name "Physics": it must contain only lowercase letters, digits, underscores and hyphens, not start with a hyphen, and have at most 64 characters`},
		{name: "leading hyphen", accounts: []Account{{Name: "-physics"}}, wantErr: `invalid account name "-physics"`},
		{name: "empty name", accounts: []Account{{Name: ""}}, wantErr: `invalid account name ""`},
		{name: "too long", accounts: []Account{{Name: strings.Repeat("a", 65)}}, wantErr: "invalid account name"},
		{name: "root", accounts: []Account{{Name: "root"}}, wantErr: `account "root" exists in every cluster: declare accounts below it`},
		{name: "duplicate", accounts: []Account{{Name: "physics"}, {Name: "physics"}}, wantErr: `duplicate account "physics"`},
		{name: "parent declared later", accounts: []Account{{Name: "theory", Parent: "physics"}, {Name: "physics"}},
			wantErr: `account "theory": parent "physics" must be declared before it`},
		{name: "own parent", accounts: []Account{{Name: "physics", Parent: "physics"}},
			wantErr: `account "physics": parent "physics" must be declared before it`},
		{name: "undeclared parent", accounts: []Account{{Name: "theory", Parent: "physic"}},
			wantErr: `account "theory": parent "physic" is not declared in accounts`},
		{name: "reserved limit", accounts: []Account{{Name: "physics", Limits: Limits{"Parent": "root"}}},
			wantErr: `account "physics": invalid limit "Parent": it must be a sacctmgr option other than name, names, parent, cluster, clusters`},
		{name: "cluster limit", accounts: []Account{{Name: "physics", Limits: Limits{"clusters": "other"}}}, wantErr: `invalid limit "clusters"`},
		{name: "limit with =", accounts: []Account{{Name: "physics", Limits: Limits{"GrpTRES=cpu": "4"}}}, wantErr: `invalid limit "GrpTRES=cpu"`},
		{name: "empty limit", accounts: []Account{{Name: "physics", Limits: Limits{"MaxJobs": ""}}}, wantErr: `account "physics": limit MaxJobs must not be empty`},
		{name: "undeclared account", accounts: []Account{{Name: "physics"}}, users: []User{{Name: "alice", Accounts: []string{"physic"}}},
			wantErr: `user "alice": accounts: account "physic" is not declared in accounts`},
		{name: "root account", users: []User{{Name: "alice", Accounts: []string{"root"}}},
			wantErr: `user "alice": accounts: account "root" is not declared in accounts`},
		{name: "account twice", accounts: []Account{{Name: "physics"}}, users: []User{{Name: "alice", Accounts: []string{"physics", "physics"}}},
			wantErr: `user "alice": accounts: account "physics" is listed twice`},
		{name: "undeclared coordinator account", accounts: []Account{{Name: "physics"}}, users: []User{{Name: "alice", Accounts: []string{"physics"}, Coordinator: []string{"chemistry"}}},
			wantErr: `user "alice": coordinator: account "chemistry" is not declared in accounts`},
		{name: "coordinator twice", accounts: []Account{{Name: "physics"}}, users: []User{{Name: "alice", Accounts: []string{"physics"}, Coordinator: []string{"physics", "physics"}}},
			wantErr: `user "alice": coordinator: account "physics" is listed twice`},
		{name: "invalid admin level", accounts: []Account{{Name: "physics"}}, users: []User{{Name: "alice", Accounts: []string{"physics"}, AdminLevel: "superuser"}},
			wantErr: `user "alice": adminLevel must be "operator" or "admin", got "superuser"`},
		{name: "coordinator without accounts", accounts: []Account{{Name: "physics"}}, users: []User{{Name: "alice", Coordinator: []string{"physics"}}},
			wantErr: `user "alice": coordinator and adminLevel need accounts: sacctmgr creates a Slurm user only with an account`},
		{name: "admin level without accounts", users: []User{{Name: "alice", AdminLevel: AdminOperator}},
			wantErr: `user "alice": coordinator and adminLevel need accounts`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes := tt.nodes
			if nodes == nil {
				nodes = withDB
			}
			cfg := &Cluster{Kind: "Cluster", Name: DefaultClusterName, Nodes: nodes, Accounts: tt.accounts, Users: tt.users}
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
