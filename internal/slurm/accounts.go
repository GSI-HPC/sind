// SPDX-License-Identifier: LGPL-3.0-or-later

package slurm

import (
	"strings"

	"github.com/GSI-HPC/sind/pkg/config"
)

// AccountCommands returns the sacctmgr commands, without "sacctmgr -i",
// that create the cluster's Slurm accounts and the users' associations and
// roles, in the order they must run: the accounts, parents first, as
// declared; then each user with accounts, default account and admin level;
// then the coordinators, who must exist as Slurm users.
func AccountCommands(accounts []config.Account, users []config.User) [][]string {
	var cmds [][]string
	for _, a := range accounts {
		cmd := []string{"add", "account", a.Name}
		if a.Parent != "" {
			cmd = append(cmd, "parent="+a.Parent)
		}
		for _, key := range a.Limits.Keys() {
			cmd = append(cmd, key+"="+a.Limits[key])
		}
		cmds = append(cmds, cmd)
	}
	for _, u := range users {
		if len(u.Accounts) == 0 {
			continue
		}
		cmd := []string{"add", "user", u.Name, "account=" + strings.Join(u.Accounts, ","), "defaultaccount=" + u.Accounts[0]}
		if u.AdminLevel != "" {
			cmd = append(cmd, "adminlevel="+string(u.AdminLevel))
		}
		cmds = append(cmds, cmd)
	}
	for _, u := range users {
		if len(u.Coordinator) > 0 {
			cmds = append(cmds, []string{"add", "coordinator", "account=" + strings.Join(u.Coordinator, ","), "names=" + u.Name})
		}
	}
	return cmds
}
