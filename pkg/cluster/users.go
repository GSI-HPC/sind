// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// HomeMountPath is where the nodes of a cluster with users mount the
// cluster's home volume, which holds the users' home directories.
const HomeMountPath = "/home"

// HomeDir returns the home directory of a cluster user.
func HomeDir(user string) string {
	return HomeMountPath + "/" + user
}

// LinuxUser is a user account sind creates on the nodes that get the
// cluster users.
type LinuxUser struct {
	Name string
	UID  int
	GID  int // primary group
}

// LinuxGroup is a group sind creates on the nodes that get the cluster
// users.
type LinuxGroup struct {
	Name string
	GID  int
	// Members are the users that have the group as a supplementary group.
	// As in /etc/group, users with it as their primary group are not
	// listed.
	Members []string
}

// LinuxUsers are the user accounts and groups of a cluster (users and
// groups), with every ID resolved: each user without a primary group has a
// private group of its own name with gid = uid.
type LinuxUsers struct {
	Users  []LinuxUser
	Groups []LinuxGroup
}

// IsEmpty reports whether the cluster has neither users nor groups.
func (l LinuxUsers) IsEmpty() bool {
	return len(l.Users) == 0 && len(l.Groups) == 0
}

// supplementaryGroups returns the groups that list user as a member.
func (l LinuxUsers) supplementaryGroups(user string) []string {
	var groups []string
	for _, g := range l.Groups {
		if slices.Contains(g.Members, user) {
			groups = append(groups, g.Name)
		}
	}
	return groups
}

// NewLinuxUsers resolves the users and groups of a cluster config, after
// ApplyDefaults has assigned their IDs: the private groups, in user order,
// then the declared groups with their members.
func NewLinuxUsers(cfg *config.Cluster) LinuxUsers {
	var l LinuxUsers
	gids := make(map[string]int, len(cfg.Groups))
	for _, g := range cfg.Groups {
		gids[g.Name] = g.GID
	}
	for _, u := range cfg.Users {
		gid := u.UID
		if u.Group != "" {
			gid = gids[u.Group]
		} else {
			l.Groups = append(l.Groups, LinuxGroup{Name: u.Name, GID: u.UID})
		}
		l.Users = append(l.Users, LinuxUser{Name: u.Name, UID: u.UID, GID: gid})
	}
	for _, g := range cfg.Groups {
		group := LinuxGroup{Name: g.Name, GID: g.GID}
		for _, u := range cfg.Users {
			if slices.Contains(u.Groups, g.Name) {
				group.Members = append(group.Members, u.Name)
			}
		}
		l.Groups = append(l.Groups, group)
	}
	return l
}

// Labels returns the labels that record the users and groups on every node
// container (LabelUsers and LabelGroups), so that workers added later get
// the same ones. Both are space-separated entries, not comma-separated:
// docker ps joins all labels of a container with commas. Both are set, empty
// without users or groups, so that no image label of that name shows
// through.
//
//	sind.users   name:uid:gid ...          alice:1000:1000 bob:2002:3000
//	sind.groups  name:gid[:member+...] ... alice:1000 hpc:3000:alice
func (l LinuxUsers) Labels() docker.Labels {
	labels := docker.Labels{LabelUsers: "", LabelGroups: ""}
	if len(l.Users) > 0 {
		entries := make([]string, len(l.Users))
		for i, u := range l.Users {
			entries[i] = u.Name + ":" + strconv.Itoa(u.UID) + ":" + strconv.Itoa(u.GID)
		}
		labels[LabelUsers] = strings.Join(entries, " ")
	}
	if len(l.Groups) > 0 {
		entries := make([]string, len(l.Groups))
		for i, g := range l.Groups {
			entries[i] = g.Name + ":" + strconv.Itoa(g.GID)
			if len(g.Members) > 0 {
				entries[i] += ":" + strings.Join(g.Members, "+")
			}
		}
		labels[LabelGroups] = strings.Join(entries, " ")
	}
	return labels
}

// LinuxUsersFromLabels returns the users and groups a node container's
// LabelUsers and LabelGroups record (see LinuxUsers.Labels), empty when it
// has neither.
func LinuxUsersFromLabels(labels docker.Labels) (LinuxUsers, error) {
	var l LinuxUsers
	usersErr := fmt.Errorf("invalid %s label %q: want name:uid:gid entries", LabelUsers, labels[LabelUsers])
	for entry := range strings.FieldsSeq(labels[LabelUsers]) {
		fields := strings.Split(entry, ":")
		if len(fields) != 3 || config.CheckUserName(fields[0]) != nil {
			return LinuxUsers{}, usersErr
		}
		uid, uidErr := strconv.Atoi(fields[1])
		gid, gidErr := strconv.Atoi(fields[2])
		if uidErr != nil || gidErr != nil {
			return LinuxUsers{}, usersErr
		}
		l.Users = append(l.Users, LinuxUser{Name: fields[0], UID: uid, GID: gid})
	}
	groupsErr := fmt.Errorf("invalid %s label %q: want name:gid[:member+...] entries", LabelGroups, labels[LabelGroups])
	for entry := range strings.FieldsSeq(labels[LabelGroups]) {
		fields := strings.Split(entry, ":")
		if len(fields) < 2 || len(fields) > 3 || config.CheckGroupName(fields[0]) != nil {
			return LinuxUsers{}, groupsErr
		}
		gid, err := strconv.Atoi(fields[1])
		if err != nil {
			return LinuxUsers{}, groupsErr
		}
		group := LinuxGroup{Name: fields[0], GID: gid}
		if len(fields) == 3 {
			group.Members = strings.Split(fields[2], "+")
			for _, m := range group.Members {
				if config.CheckUserName(m) != nil {
					return LinuxUsers{}, groupsErr
				}
			}
		}
		l.Groups = append(l.Groups, group)
	}
	return l, nil
}

// addUsersScript returns the shell script that creates the groups, then the
// users, each with its IDs, primary and supplementary groups. The home
// directory is on the shared home volume, which createHomes fills once per
// cluster. The names are checked user and group names, safe to use
// unquoted.
func addUsersScript(l LinuxUsers) string {
	var b strings.Builder
	for _, g := range l.Groups {
		fmt.Fprintf(&b, "groupadd --gid %d %s\n", g.GID, g.Name)
	}
	for _, u := range l.Users {
		fmt.Fprintf(&b, "useradd --uid %d --gid %d", u.UID, u.GID)
		if groups := l.supplementaryGroups(u.Name); len(groups) > 0 {
			b.WriteString(" --groups " + strings.Join(groups, ","))
		}
		fmt.Fprintf(&b, " --home-dir %s --no-create-home --shell /bin/bash %s\n", HomeDir(u.Name), u.Name)
	}
	return b.String()
}

// addUsersStep returns the node setup step (see nodeSetupSteps) that
// creates the cluster's groups and users on a node. Every node runs the
// same commands with the same IDs, so a user has one UID and GID across the
// cluster, as munge and Slurm require.
func addUsersStep(l LinuxUsers) setupStep {
	return setupStep{name: "users", what: "adding users", script: addUsersScript(l)}
}

// createHomesScript creates, for each name, uid and gid triple in its
// arguments after the first, the user's home directory from /etc/skel, and
// authorizes the SSH public key in the first argument to log in as the
// user. It sets ownership by number, so it runs on any node, whether or not
// the node has the users.
const createHomesScript = `key=$1
shift
while [ $# -gt 0 ]; do
	home=` + HomeMountPath + `/$1
	mkdir -p "$home/.ssh"
	cp -a /etc/skel/. "$home"
	printf '%s\n' "$key" > "$home/.ssh/authorized_keys"
	chmod 700 "$home" "$home/.ssh"
	chmod 600 "$home/.ssh/authorized_keys"
	chown -R "$2:$3" "$home"
	shift 3
done`

// createHomes creates the users' home directories on the home volume, which
// every node mounts, so it runs once. The realm's SSH key may log in as each
// user, as it may as root.
func createHomes(ctx context.Context, client *docker.Client, container docker.ContainerName, users []LinuxUser, sshPubKey string) error {
	args := []string{"sh", "-ec", createHomesScript, "sh", strings.TrimSpace(sshPubKey)}
	for _, u := range users {
		args = append(args, u.Name, strconv.Itoa(u.UID), strconv.Itoa(u.GID))
	}
	if _, err := client.Exec(ctx, container, args...); err != nil {
		return fmt.Errorf("creating home directories: %w", err)
	}
	return nil
}
