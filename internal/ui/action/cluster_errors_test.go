// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNameError(t *testing.T) {
	for _, tc := range []struct{ kind, name, want string }{
		{"cluster", "hpc", ""},
		{"realm", "ssh", ""},
		{"cluster", "", "give the cluster a name, such as hpc"},
		{"realm", "", "give the realm a name, such as ci"},
		{"cluster", strings.Repeat("a", 64), "use at most 63 characters; this name has 64"},
		{"cluster", "My Cluster", "use only lowercase letters, digits and -, no spaces, such as my-cluster"},
		{"cluster", "Bad_Name", "use only lowercase letters, digits and -, no _, such as bad-name"},
		{"cluster", "__", "use only lowercase letters, digits and -, no _"},
		{"cluster", "café", "use only lowercase letters, digits and -, no é, such as caf"},
		{"realm", "CI", "use lowercase letters, such as ci"},
		{"cluster", "-dev-", "start and end with a letter or a digit, such as dev"},
		{"cluster", "ssh", "ssh is the name of the SSH relay of the realm; pick another name"},
		{"cluster", "x-ssh", "a name may not end in -ssh, as the SSH relay of a realm uses it; pick another name"},
	} {
		t.Run(tc.kind+" "+tc.name, func(t *testing.T) {
			err := nameError(tc.kind, tc.name)
			if tc.want == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, tc.want, err.Error())
		})
	}
}

func TestSlug(t *testing.T) {
	assert.Equal(t, "my-cluster-2", slug("  My Cluster #2 "))
	assert.Empty(t, slug("__"))
	assert.Equal(t, strings.Repeat("a", 62), slug(strings.Repeat("a", 62)+"-b"), "cut to 63, without a hyphen at the end")
}

// TestBuildCreateCluster_ConfigProblems makes each error of sind's check of
// the config that a form can cause, so that a change of sind's words shows
// here.
func TestBuildCreateCluster_ConfigProblems(t *testing.T) {
	withAccount := func(kv ...string) Form {
		return rows(rows(valid("db", "on"), AccountRows, "a0", "name", "a"), UserRows, "u0", append([]string{"name", "alice"}, kv...)...)
	}
	withGroup := func(kv ...string) Form {
		return rows(rows(valid(), GroupRows, "g0", "name", "hpc"), UserRows, "u0", append([]string{"name", "alice"}, kv...)...)
	}
	for _, tc := range []struct {
		name string
		f    Form
		want Problem
	}{
		{"account", withAccount("accounts", "ghost"), Problem{Field: "u0-accounts", Label: "Accounts of alice", Text: "add the account ghost under Slurm accounts first, or remove it from alice"}},
		{"coordinator", withAccount("accounts", "a", "coordinator", "ghost"), Problem{Field: "u0-coordinator", Label: "Coordinator of alice", Text: "add the account ghost under Slurm accounts first, or remove it from alice"}},
		{"account twice", withAccount("accounts", "a,a"), Problem{Field: "u0-accounts", Label: "Accounts of alice", Text: "a is there twice; remove one"}},
		{"coordinator without account", withAccount("coordinator", "a"), Problem{Field: "u0-accounts", Label: "Accounts of alice", Text: "give alice an account first: a coordinator and an admin level need one"}},
		{"group", withGroup("group", "nope"), Problem{Field: "u0-group", Label: "Primary group of alice", Text: "add the group nope under Groups first, or leave the primary group empty"}},
		{"groups", withGroup("groups", "nope"), Problem{Field: "u0-groups", Label: "Groups of alice", Text: "add the group nope under Groups first, or remove it from alice"}},
		{"primary in groups", withGroup("group", "hpc", "groups", "hpc"), Problem{Field: "u0-groups", Label: "Groups of alice", Text: "hpc is the primary group of alice already; remove it from Groups"}},
		{"groups twice", withGroup("groups", "hpc,hpc"), Problem{Field: "u0-groups", Label: "Groups of alice", Text: "hpc is there twice; remove one"}},
		{"user twice", rows(withGroup(), UserRows, "u1", "name", "alice"), Problem{Field: "u1-name", Label: "User", Text: "another user is named alice; rename one of them"}},
		{"group twice", rows(withGroup(), GroupRows, "g1", "name", "hpc"), Problem{Field: "g1-name", Label: "Group", Text: "another group is named hpc; rename one of them"}},
		{"account twice", rows(withAccount(), AccountRows, "a1", "name", "a"), Problem{Field: "a1-name", Label: "Account", Text: "another account is named a; rename one of them"}},
		{"uid twice", rows(withGroup("uid", "2000"), UserRows, "u1", "name", "bob", "uid", "2000"), Problem{Field: "u1-uid", Label: "UID of bob", Text: "alice has the UID 2000 already; pick another, or leave it empty for the next free one"}},
		{"private group", rows(withGroup(), GroupRows, "g1", "name", "alice"), Problem{Field: "g1-name", Label: "Group", Text: "the user alice has a private group of that name; rename the group, or make it the primary group of alice"}},
		{"parent", rows(valid("db", "on"), AccountRows, "a0", "name", "b", "parent", "ghost"), Problem{Field: "a0-parent", Label: "Parent of b", Text: "add the account ghost first, or leave the parent empty for root"}},
		{"parent after", rows(rows(valid("db", "on"), AccountRows, "a0", "name", "b", "parent", "a"), AccountRows, "a1", "name", "a"), Problem{Field: "a0-parent", Label: "Parent of b", Text: "sind creates a parent first, so a must come above b; remove b and add it again"}},
		{"root", rows(valid("db", "on"), AccountRows, "a0", "name", "root"), Problem{Field: "a0-name", Label: "Account", Text: "root is there in every cluster; pick another name, and an empty parent puts the account below root"}},
		{"limit empty", rows(valid("db", "on"), AccountRows, "a0", "name", "a", "lk", "MaxJobs", "lv", ""), Problem{Field: "a0-lk", Label: "Limits of a", Text: "give MaxJobs a value, or remove it"}},
		{"other", rows(withGroup("uid", "2000"), GroupRows, "g1", "name", "lab", "gid", "2000"), Problem{Label: "Config", Text: `group "lab" and the private group of user "alice" have the same gid 2000`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := build(t, "create-cluster", tc.f)
			require.NoError(t, err)
			require.Len(t, cmd.Problems, 1, "%+v", cmd.Problems)
			assert.Equal(t, tc.want, cmd.Problems[0])
		})
	}
}

func TestConfigProblem_NoRow(t *testing.T) {
	assert.Equal(t, Problem{Label: "Accounts of mia", Text: "add the account x under Slurm accounts first, or remove it from mia"},
		configProblem(form(), errors.New(`user "mia": accounts: account "x" is not declared in accounts`)), "no row of that name: no field")
}
