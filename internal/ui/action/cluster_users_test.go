// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rows adds the row key of r to the form, with the fields of attr and value
// pairs.
func rows(f Form, r Rows, key string, kv ...string) Form {
	f.Add(r.List, key)
	for i := 0; i+1 < len(kv); i += 2 {
		f.Add(key+"-"+kv[i], kv[i+1])
	}
	return f
}

func TestBuildCreateCluster_UsersGroupsAccounts(t *testing.T) {
	f := valid("db", "on")
	rows(f, UserRows, "u0", "name", "alice", "uid", "2001", "accounts", "physics, theory", "groups", "hpc", "coordinator", "physics", "admin", "operator")
	rows(f, UserRows, "uab12cd", "name", "bob", "group", "hpc")
	rows(f, UserRows, "u2", "name", " ", "uid", "")
	rows(f, GroupRows, "g0", "name", "hpc", "gid", "3000")
	rows(f, GroupRows, "g1")
	rows(f, AccountRows, "a0", "name", "physics", "lk", "maxjobs", "lv", "4", "lk", "", "lv", "", "lk", "GrpTRES", "lv", "cpu=8,mem=16G")
	rows(f, AccountRows, "a1", "name", "theory", "parent", "physics", "lk", "Description", "lv", "Theory group")
	rows(f, AccountRows, "a2")

	cmd, err := build(t, "create-cluster", f)
	require.NoError(t, err)
	require.Empty(t, cmd.Problems)
	assert.Contains(t, cmd.Config.Content, `users:
  - name: alice
    uid: 2001
    groups:
      - hpc
    accounts:
      - physics
      - theory
    coordinator:
      - physics
    adminLevel: operator
  - name: bob
    group: hpc
groups:
  - name: hpc
    gid: 3000
accounts:
  - name: physics
    limits:
      GrpTRES: cpu=8,mem=16G
      MaxJobs: "4"
  - name: theory
    parent: physics
    limits:
      Description: Theory group
`)
}

func TestBuildCreateCluster_RowProblems(t *testing.T) {
	for _, tc := range []struct {
		name        string
		f           Form
		field, want string
		def         bool
	}{
		{"user name", rows(valid(), UserRows, "u0", "name", "Alice"), "u0-name", "invalid user name", false},
		{"uid", rows(valid(), UserRows, "u0", "name", "alice", "uid", "12"), "u0-uid", "write a number from 1000", true},
		{"uid text", rows(valid(), UserRows, "u0", "name", "alice", "uid", "x"), "u0-uid", "write a number from 1000", true},
		{"admin", rows(rows(valid("db", "on"), AccountRows, "a0", "name", "a"), UserRows, "u0", "name", "alice", "accounts", "a", "admin", "root"), "u0-admin", "pick none, operator or admin", true},
		{"group name", rows(valid(), GroupRows, "g0", "name", "-x"), "g0-name", "invalid group name", false},
		{"gid", rows(valid(), GroupRows, "g0", "name", "hpc", "gid", "65534000000"), "g0-gid", "write a number from 1000", true},
		{"limit", rows(valid("db", "on"), AccountRows, "a0", "name", "a", "lk", "Account", "lv", "x"), "a0-lk", `"Account" is no limit that sacctmgr takes`, false},
		{"row key", rows(valid(), UserRows, "api"), "", `the row "api" is not one that the dialog made`, false},
		{"row key twice", rows(rows(valid(), GroupRows, "g0"), GroupRows, "g0"), "", `the row "g0" is not one`, false},
		{"accounts without db", rows(valid(), AccountRows, "a0", "name", "physics"), "db", "the Slurm accounts need the db node", true},
		{"admin without db", rows(valid(), UserRows, "u0", "name", "alice", "admin", "admin"), "db", "the Slurm accounts need the db node", true},
		{"accounts unmanaged", rows(valid("controller-unmanaged", "on", "db", "on"), AccountRows, "a0", "name", "physics"), "controller-unmanaged", "a managed cluster with a db node", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := build(t, "create-cluster", tc.f)
			require.NoError(t, err)
			require.Len(t, cmd.Problems, 1, "%+v", cmd.Problems)
			assert.Equal(t, tc.field, cmd.Problems[0].Field)
			assert.Contains(t, cmd.Problems[0].Text, tc.want)
			assert.Equal(t, tc.def, cmd.Problems[0].Default)
			if tc.field == "db" || tc.field == "controller-unmanaged" {
				assert.NotContains(t, cmd.Config.Content, "accounts:", "the plan leaves out what it cannot take")
				assert.NotContains(t, cmd.Config.Content, "adminLevel:")
			}
		})
	}
}

func TestBuildCreateCluster_RowsTheConfigChecks(t *testing.T) {
	f := valid("db", "on")
	rows(f, UserRows, "u0", "name", "alice", "group", "nope", "accounts", "ghost")
	rows(f, AccountRows, "a0", "name", "Physics")
	cmd, err := build(t, "create-cluster", f)
	require.NoError(t, err)
	var texts []string
	for _, p := range cmd.Problems {
		assert.Equal(t, "Config", p.Label)
		texts = append(texts, p.Text)
	}
	require.Len(t, texts, 2, "%q", texts)
	assert.Contains(t, texts[0], `group "nope"`)
	assert.Contains(t, texts[1], `invalid account name "Physics"`)
}

func TestBuildCreateCluster_ManyRows(t *testing.T) {
	f := valid()
	for i := range maxRows + 1 {
		rows(f, GroupRows, fmt.Sprintf("g%d", i), "name", fmt.Sprintf("g%d", i))
	}
	cmd, err := build(t, "create-cluster", f)
	require.NoError(t, err)
	require.Len(t, cmd.Problems, 1)
	assert.Equal(t, Problem{Field: "g64-name", Label: "Groups", Text: "a cluster here has at most 64 groups"}, cmd.Problems[0])
}

func TestUserFieldsOf(t *testing.T) {
	f := rows(rows(rows(form(), UserRows, "u0"), GroupRows, "g0"), AccountRows, "a0")
	f.Add("ar", "api")
	fields := UserFieldsOf(f)
	assert.Equal(t, []string{"u0-name", "u0-uid", "u0-accounts", "u0-group", "u0-groups", "u0-coordinator", "u0-admin", "g0-name", "g0-gid", "a0-name", "a0-parent", "a0-lk"}, fields)
	assert.Contains(t, ClusterFieldsOf(f), "g0-gid")
}
