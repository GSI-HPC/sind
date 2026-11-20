// SPDX-License-Identifier: LGPL-3.0-or-later

package slurm

import (
	"testing"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/stretchr/testify/assert"
)

func TestAccountCommands(t *testing.T) {
	accounts := []config.Account{
		{Name: "physics", Limits: config.Limits{"GrpTRES": "cpu=4", "Fairshare": "10"}},
		{Name: "theory", Parent: "physics", Limits: config.Limits{"MaxJobs": "1"}},
		{Name: "chemistry", Parent: "root"},
	}
	users := []config.User{
		{Name: "alice", Accounts: []string{"theory"}},
		{Name: "bob", Accounts: []string{"physics", "theory"}, Coordinator: []string{"physics"}},
		{Name: "carol", Accounts: []string{"physics"}, AdminLevel: config.AdminOperator, Coordinator: []string{"physics", "chemistry"}},
		{Name: "dave"},
	}

	assert.Equal(t, [][]string{
		{"add", "account", "physics", "Fairshare=10", "GrpTRES=cpu=4"},
		{"add", "account", "theory", "parent=physics", "MaxJobs=1"},
		{"add", "account", "chemistry", "parent=root"},
		{"add", "user", "alice", "account=theory", "defaultaccount=theory"},
		{"add", "user", "bob", "account=physics,theory", "defaultaccount=physics"},
		{"add", "user", "carol", "account=physics", "defaultaccount=physics", "adminlevel=operator"},
		{"add", "coordinator", "account=physics", "names=bob"},
		{"add", "coordinator", "account=physics,chemistry", "names=carol"},
	}, AccountCommands(accounts, users))
}

func TestAccountCommands_None(t *testing.T) {
	assert.Nil(t, AccountCommands(nil, []config.User{{Name: "alice"}}))
}
