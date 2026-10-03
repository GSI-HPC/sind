// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_Users(t *testing.T) {
	input := `kind: Cluster
groups:
  - physics
  - name: hpc
    gid: 3000
users:
  - alice
  - name: bob
    uid: 2001
    group: hpc
    groups: [physics]`

	cfg, err := Parse([]byte(input))
	require.NoError(t, err)
	assert.Equal(t, []User{{Name: "alice"}, {Name: "bob", UID: 2001, Group: "hpc", Groups: []string{"physics"}}}, cfg.Users)
	assert.Equal(t, []Group{{Name: "physics"}, {Name: "hpc", GID: 3000}}, cfg.Groups)
}

func TestParse_UsersErrors(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name:    "unknown user field",
			input:   "kind: Cluster\nusers:\n  - name: alice\n    shell: /bin/zsh",
			wantErr: `user must be a name or an object: json: unknown field "shell"`,
		},
		{
			name:    "user is a list",
			input:   "kind: Cluster\nusers:\n  - [alice]",
			wantErr: "user must be a name or an object",
		},
		{
			name:    "uid is not a number",
			input:   "kind: Cluster\nusers:\n  - name: alice\n    uid: high",
			wantErr: "user must be a name or an object",
		},
		{
			name:    "groups is not a list",
			input:   "kind: Cluster\nusers:\n  - name: alice\n    groups: hpc",
			wantErr: "user must be a name or an object",
		},
		{
			name:    "unknown group field",
			input:   "kind: Cluster\ngroups:\n  - name: hpc\n    members: [alice]",
			wantErr: `group must be a name or an object with name and gid: json: unknown field "members"`,
		},
		{
			name:    "gid is not a number",
			input:   "kind: Cluster\ngroups:\n  - name: hpc\n    gid: high",
			wantErr: "group must be a name or an object with name and gid",
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

func TestApplyDefaults_GroupGIDs(t *testing.T) {
	tests := []struct {
		name   string
		users  []User
		groups []Group
		uids   []int
		gids   []int
	}{
		{"groups only", nil, []Group{{Name: "a"}, {Name: "b"}}, nil, []int{1000, 1001}},
		{"after the users", []User{{Name: "u"}, {Name: "v", UID: 1002}}, []Group{{Name: "a"}, {Name: "b"}}, []int{1000, 1002}, []int{1001, 1003}},
		{"explicit gids kept", nil, []Group{{Name: "a", GID: 3000}, {Name: "b"}}, nil, []int{3000, 1000}},
		{"users skip explicit gids", []User{{Name: "u"}}, []Group{{Name: "a", GID: 1000}}, []int{1001}, []int{1000}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Cluster{Kind: "Cluster", Name: DefaultClusterName, Users: tt.users, Groups: tt.groups}
			cfg.ApplyDefaults()
			var uids, gids []int
			for _, u := range cfg.Users {
				uids = append(uids, u.UID)
			}
			for _, g := range cfg.Groups {
				gids = append(gids, g.GID)
			}
			assert.Equal(t, tt.uids, uids)
			assert.Equal(t, tt.gids, gids)
		})
	}
}

func TestAssignIDs_SkipsReserved(t *testing.T) {
	// With every ID up to 65533 taken, the next free one is 65536.
	var users []User
	for id := MinUID; id < ReservedIDs[0]; id++ {
		users = append(users, User{UID: id})
	}
	users = append(users, User{Name: "last"})
	groups := []Group{{Name: "hpc"}}
	assignIDs(users, groups)
	assert.Equal(t, 65536, users[len(users)-1].UID)
	assert.Equal(t, 65537, groups[0].GID)
}

func TestApplyDefaults_UserUIDs(t *testing.T) {
	tests := []struct {
		name  string
		users []User
		want  []int
	}{
		{"none", nil, nil},
		{"sequential from MinUID", []User{{Name: "a"}, {Name: "b"}}, []int{1000, 1001}},
		{"explicit uids kept", []User{{Name: "a", UID: 2001}, {Name: "b", UID: 1000}}, []int{2001, 1000}},
		{"skips explicit uids", []User{{Name: "a"}, {Name: "b", UID: 1001}, {Name: "c"}, {Name: "d", UID: 1000}}, []int{1002, 1001, 1003, 1000}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Cluster{Kind: "Cluster", Name: DefaultClusterName, Users: tt.users}
			cfg.ApplyDefaults()
			var got []int
			for _, u := range cfg.Users {
				got = append(got, u.UID)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValidate_Users(t *testing.T) {
	tests := []struct {
		name    string
		users   []User
		wantErr string
	}{
		{name: "none"},
		{name: "valid", users: []User{{Name: "alice"}, {Name: "_svc-1", UID: MaxUID}, {Name: "bob", UID: 1000}}},
		{name: "longest name", users: []User{{Name: "a" + strings.Repeat("b", 31)}}},
		{name: "empty name", users: []User{{Name: ""}}, wantErr: `invalid user name ""`},
		{name: "uppercase", users: []User{{Name: "Alice"}}, wantErr: `invalid user name "Alice": it must start with a lowercase letter or underscore`},
		{name: "leading digit", users: []User{{Name: "1alice"}}, wantErr: `invalid user name "1alice"`},
		{name: "leading hyphen", users: []User{{Name: "-alice"}}, wantErr: `invalid user name "-alice"`},
		{name: "dot", users: []User{{Name: "a.b"}}, wantErr: `invalid user name "a.b"`},
		{name: "too long", users: []User{{Name: "a" + strings.Repeat("b", 32)}}, wantErr: "have at most 32 characters"},
		{name: "duplicate name", users: []User{{Name: "alice"}, {Name: "alice"}}, wantErr: `duplicate user "alice"`},
		{name: "system uid", users: []User{{Name: "alice", UID: 999}}, wantErr: `user "alice": uid must be between 1000 and 2147483647, got 999`},
		{name: "negative uid", users: []User{{Name: "alice", UID: -1}}, wantErr: "got -1"},
		{name: "uid too large", users: []User{{Name: "alice", UID: MaxUID + 1}}, wantErr: "got 2147483648"},
		{name: "nobody's uid", users: []User{{Name: "alice", UID: 65534}}, wantErr: `user "alice": uid 65534 is reserved: 65534 is the image's nobody user and group, and 65535 the 16-bit -1`},
		{name: "16-bit -1", users: []User{{Name: "alice", UID: 65535}}, wantErr: `user "alice": uid 65535 is reserved`},
		{name: "above the 16-bit range", users: []User{{Name: "alice", UID: 65536}}},
		{name: "duplicate uid", users: []User{{Name: "alice", UID: 2001}, {Name: "bob", UID: 2001}}, wantErr: `users "alice" and "bob" have the same uid 2001`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Cluster{Kind: "Cluster", Name: DefaultClusterName, Users: tt.users}
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

func TestValidate_Groups(t *testing.T) {
	tests := []struct {
		name    string
		users   []User
		groups  []Group
		wantErr string
	}{
		{name: "valid", users: []User{{Name: "alice", Groups: []string{"hpc", "physics"}}, {Name: "bob", Group: "hpc"}}, groups: []Group{{Name: "hpc"}, {Name: "physics", GID: 3000}}},
		{name: "declared primary group of the user's name", users: []User{{Name: "alice", Group: "alice"}}, groups: []Group{{Name: "alice", GID: 5000}}},
		{name: "invalid name", groups: []Group{{Name: "HPC"}}, wantErr: `invalid group name "HPC": it must start with a lowercase letter or underscore, contain only lowercase letters, digits, underscores and hyphens, and have at most 32 characters`},
		{name: "duplicate", groups: []Group{{Name: "hpc"}, {Name: "hpc"}}, wantErr: `duplicate group "hpc"`},
		{name: "system gid", groups: []Group{{Name: "hpc", GID: 100}}, wantErr: `group "hpc": gid must be between 1000 and 2147483647, got 100`},
		{name: "nobody's gid", groups: []Group{{Name: "hpc", GID: 65534}}, wantErr: `group "hpc": gid 65534 is reserved: 65534 is the image's nobody user and group, and 65535 the 16-bit -1`},
		{name: "duplicate gid", groups: []Group{{Name: "hpc", GID: 3000}, {Name: "physics", GID: 3000}}, wantErr: `group "hpc" and group "physics" have the same gid 3000`},
		{name: "gid of a private group", users: []User{{Name: "alice", UID: 3000}}, groups: []Group{{Name: "hpc", GID: 3000}}, wantErr: `group "hpc" and the private group of user "alice" have the same gid 3000`},
		{name: "private group shares a gid with a primary group user", users: []User{{Name: "alice", UID: 3000, Group: "hpc"}, {Name: "bob", UID: 3001}}, groups: []Group{{Name: "hpc", GID: 3001}}, wantErr: `group "hpc" and the private group of user "bob" have the same gid 3001`},
		{name: "clashes with a private group", users: []User{{Name: "alice"}}, groups: []Group{{Name: "alice"}}, wantErr: `group "alice" clashes with the private group of user "alice": set group: alice on the user, or rename the group`},
		{name: "undeclared primary group", users: []User{{Name: "alice", Group: "hpc"}}, wantErr: `user "alice": group "hpc" is not declared in groups`},
		{name: "undeclared supplementary group", users: []User{{Name: "alice", Groups: []string{"hpc"}}}, wantErr: `user "alice": supplementary group "hpc" is not declared in groups`},
		{name: "private group as supplementary group", users: []User{{Name: "alice"}, {Name: "bob", Groups: []string{"alice"}}}, wantErr: `user "bob": supplementary group "alice" is not declared in groups`},
		{name: "primary group also supplementary", users: []User{{Name: "alice", Group: "hpc", Groups: []string{"hpc"}}}, groups: []Group{{Name: "hpc"}}, wantErr: `user "alice": "hpc" is its primary group, not a supplementary one`},
		{name: "supplementary group twice", users: []User{{Name: "alice", Groups: []string{"hpc", "hpc"}}}, groups: []Group{{Name: "hpc"}}, wantErr: `user "alice": supplementary group "hpc" is listed twice`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Cluster{Kind: "Cluster", Name: DefaultClusterName, Users: tt.users, Groups: tt.groups}
			cfg.ApplyDefaults()
			err := cfg.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, tt.wantErr, err.Error())
		})
	}
}
