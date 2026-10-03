// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
)

// ID range of cluster users and groups. The IDs below MinUID belong to the
// system accounts of the node image (slurm, munge, sshd, ...).
const (
	MinUID = 1000
	MaxUID = math.MaxInt32
)

// User is a Linux user account that sind creates on the nodes of the
// cluster, with a home directory under /home.
type User struct {
	Name string `json:"name"`
	// UID is the ID of the user and, without Group, of its private group.
	// Zero assigns the lowest free ID from MinUID up (see
	// Cluster.ApplyDefaults).
	UID int `json:"uid,omitempty"`
	// Group is the user's primary group, one of the cluster's groups.
	// Empty gives the user a private group of its own name, with gid = uid.
	Group string `json:"group,omitempty"`
	// Groups are the user's supplementary groups, all among the cluster's
	// groups.
	Groups []string `json:"groups,omitempty"`
	// Accounts are the Slurm accounts the user gets associations with,
	// all among the cluster's accounts. The first is the default account.
	Accounts []string `json:"accounts,omitempty"`
	// Coordinator are the accounts the user coordinates: the user may
	// manage them and their sub-accounts.
	Coordinator []string `json:"coordinator,omitempty"`
	// AdminLevel is the user's Slurm admin level, none by default.
	AdminLevel AdminLevel `json:"adminLevel,omitempty"`
}

// UnmarshalJSON supports two YAML forms:
//   - bare string: "alice"
//   - full object: "name: alice\n  uid: 2001"
func (u *User) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		u.Name = name
		return nil
	}

	type userAlias User // prevents infinite recursion
	var alias userAlias
	if err := decodeStrict(data, &alias); err != nil {
		return fmt.Errorf("user must be a name or an object: %w", err)
	}
	*u = User(alias)
	return nil
}

// Group is a Linux group that sind creates on the nodes that get the
// cluster users, as a primary or supplementary group of the users.
type Group struct {
	Name string `json:"name"`
	// GID is the ID of the group. Zero assigns the lowest ID from MinUID up
	// that no user or group has (see Cluster.ApplyDefaults).
	GID int `json:"gid,omitempty"`
}

// UnmarshalJSON supports two YAML forms:
//   - bare string: "hpc"
//   - full object: "name: hpc\n  gid: 3000"
func (g *Group) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		g.Name = name
		return nil
	}

	type groupAlias Group // prevents infinite recursion
	var alias groupAlias
	if err := decodeStrict(data, &alias); err != nil {
		return fmt.Errorf("group must be a name or an object with name and gid: %w", err)
	}
	*g = Group(alias)
	return nil
}

// decodeStrict decodes a JSON object into v, rejecting unknown fields.
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// userNamePattern is the portable subset of Linux user and group names that
// useradd and groupadd accept on every distribution: a lowercase letter or
// underscore, then lowercase letters, digits, underscores and hyphens, at
// most 32 characters.
var userNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// assignIDs gives each user without a UID, then each group without a GID,
// the lowest ID from MinUID up that no user or group has, in list order.
// Users skip the explicit GIDs too, so that a private group (gid = uid)
// never collides with a declared group.
func assignIDs(users []User, groups []Group) {
	taken := make(map[int]bool, len(users)+len(groups))
	for _, u := range users {
		taken[u.UID] = true
	}
	for _, g := range groups {
		taken[g.GID] = true
	}
	next := MinUID
	nextFree := func() int {
		for taken[next] {
			next++
		}
		taken[next] = true
		return next
	}
	for i := range users {
		if users[i].UID == 0 {
			users[i].UID = nextFree()
		}
	}
	for i := range groups {
		if groups[i].GID == 0 {
			groups[i].GID = nextFree()
		}
	}
}

// CheckUserName reports whether name may be used as the name of a cluster
// user.
func CheckUserName(name string) error {
	return checkAccountName("user", name)
}

// CheckGroupName reports whether name may be used as the name of a cluster
// group.
func CheckGroupName(name string) error {
	return checkAccountName("group", name)
}

// checkAccountName reports whether name may be used as the name of a Linux
// user or group; kind ("user" or "group") names it in the error.
func checkAccountName(kind, name string) error {
	if !userNamePattern.MatchString(name) {
		return fmt.Errorf("invalid %s name %q: it must start with a lowercase letter or underscore, contain only lowercase letters, digits, underscores and hyphens, and have at most 32 characters", kind, name)
	}
	return nil
}

// checkID reports whether id is in the range of cluster user and group IDs;
// what names its owner and kind ("uid" or "gid") the ID in the error.
func checkID(what, kind string, id int) error {
	if id < MinUID || id > MaxUID {
		return fmt.Errorf("%s: %s must be between %d and %d, got %d", what, kind, MinUID, MaxUID, id)
	}
	return nil
}

// validateUsers checks the names and IDs of users and groups, that none of
// them repeats, and that the users' groups are among the groups. Each user
// without a primary group has a private group of its own name with
// gid = uid, which must not clash with a declared group.
func validateUsers(users []User, groups []Group) error {
	declared := make(map[string]bool, len(groups))
	gids := make(map[int]string, len(users)+len(groups))
	addGID := func(owner string, gid int) error {
		if other, ok := gids[gid]; ok {
			return fmt.Errorf("%s and %s have the same gid %d", other, owner, gid)
		}
		gids[gid] = owner
		return nil
	}
	for _, g := range groups {
		if err := CheckGroupName(g.Name); err != nil {
			return err
		}
		if declared[g.Name] {
			return fmt.Errorf("duplicate group %q", g.Name)
		}
		declared[g.Name] = true
		if err := checkID(fmt.Sprintf("group %q", g.Name), "gid", g.GID); err != nil {
			return err
		}
		if err := addGID(fmt.Sprintf("group %q", g.Name), g.GID); err != nil {
			return err
		}
	}

	names := make(map[string]bool, len(users))
	uids := make(map[int]string, len(users))
	for _, u := range users {
		if err := CheckUserName(u.Name); err != nil {
			return err
		}
		if names[u.Name] {
			return fmt.Errorf("duplicate user %q", u.Name)
		}
		names[u.Name] = true
		if err := checkID(fmt.Sprintf("user %q", u.Name), "uid", u.UID); err != nil {
			return err
		}
		if other, ok := uids[u.UID]; ok {
			return fmt.Errorf("users %q and %q have the same uid %d", other, u.Name, u.UID)
		}
		uids[u.UID] = u.Name

		if u.Group == "" {
			if declared[u.Name] {
				return fmt.Errorf("group %q clashes with the private group of user %q: set group: %s on the user, or rename the group", u.Name, u.Name, u.Name)
			}
			if err := addGID(fmt.Sprintf("the private group of user %q", u.Name), u.UID); err != nil {
				return err
			}
		} else if !declared[u.Group] {
			return fmt.Errorf("user %q: group %q is not declared in groups", u.Name, u.Group)
		}

		listed := make(map[string]bool, len(u.Groups))
		for _, g := range u.Groups {
			switch {
			case !declared[g]:
				return fmt.Errorf("user %q: supplementary group %q is not declared in groups", u.Name, g)
			case g == u.Group:
				return fmt.Errorf("user %q: %q is its primary group, not a supplementary one", u.Name, g)
			case listed[g]:
				return fmt.Errorf("user %q: supplementary group %q is listed twice", u.Name, g)
			}
			listed[g] = true
		}
	}
	return nil
}
