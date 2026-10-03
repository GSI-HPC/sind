// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// accountsCfg returns a cluster with a db node, an account tree and users
// with associations.
func accountsCfg() *config.Cluster {
	cfg := createCfg()
	cfg.Nodes = append(cfg.Nodes, config.Node{Role: config.RoleDB, Image: "img:1", CPUs: 1, Memory: "1g", TmpSize: "1g"})
	cfg.Accounts = []config.Account{{Name: "physics"}, {Name: "theory", Parent: "physics"}}
	cfg.Users = []config.User{
		{Name: "alice", UID: 1000, Accounts: []string{"theory"}},
		{Name: "bob", UID: 1001, Accounts: []string{"physics", "theory"}, Coordinator: []string{"physics"}},
	}
	return cfg
}

// accountsOnCall answers the sacctmgr calls of createSlurmAccounts: show
// cluster lists the cluster once registered reaches zero, counting down
// with each call, and every other sacctmgr call returns result.
func accountsOnCall(registered *atomic.Int32, result mock.Result) func(args []string, _ string) (mock.Result, bool) {
	return func(args []string, _ string) (mock.Result, bool) {
		if args[0] != "exec" || len(args) < 3 || args[2] != "sacctmgr" {
			return mock.Result{}, false
		}
		if slices.Contains(args, "show") {
			if registered.Add(-1) >= 0 {
				return mock.Result{}, true
			}
			return mock.Result{Stdout: "dev\n"}, true
		}
		return result, true
	}
}

// sacctmgrCalls returns the arguments of the sacctmgr -i calls, after
// "sacctmgr -i". They all run on the controller.
func sacctmgrCalls(t *testing.T, calls []mock.Call) [][]string {
	t.Helper()
	var out [][]string
	for _, c := range calls {
		if len(c.Args) > 4 && c.Args[2] == "sacctmgr" && c.Args[3] == "-i" {
			assert.Equal(t, "sind-dev-controller", c.Args[1])
			out = append(out, c.Args[4:])
		}
	}
	return out
}

func TestCreateSlurmAccounts(t *testing.T) {
	var registered atomic.Int32
	registered.Store(2) // listed on the third show
	var m mock.Executor
	override := accountsOnCall(&registered, mock.Result{})
	m.OnCall = func(args []string, stdin string) mock.Result {
		r, _ := override(args, stdin)
		return r
	}

	err := createSlurmAccounts(t.Context(), docker.NewClient(&m), mesh.DefaultRealm, accountsCfg(), time.Millisecond, nil)

	require.NoError(t, err)
	assert.Equal(t, [][]string{
		{"add", "account", "physics"},
		{"add", "account", "theory", "parent=physics"},
		{"add", "user", "alice", "account=theory", "defaultaccount=theory"},
		{"add", "user", "bob", "account=physics,theory", "defaultaccount=physics"},
		{"add", "coordinator", "account=physics", "names=bob"},
	}, sacctmgrCalls(t, m.Calls))
	// Three registration checks, then the five commands.
	require.Len(t, m.Calls, 8)
	for _, c := range m.Calls[:3] {
		assert.Equal(t, []string{"exec", "sind-dev-controller", "sacctmgr", "-n", "-P", "show", "cluster", "format=cluster"}, c.Args)
	}
}

func TestCreateSlurmAccounts_NotRegistered(t *testing.T) {
	var registered atomic.Int32
	registered.Store(1 << 30)
	var m mock.Executor
	override := accountsOnCall(&registered, mock.Result{})
	m.OnCall = func(args []string, stdin string) mock.Result {
		r, _ := override(args, stdin)
		return r
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	err := createSlurmAccounts(ctx, docker.NewClient(&m), mesh.DefaultRealm, accountsCfg(), time.Millisecond, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "waiting for slurmdbd to register the cluster: node sind-dev-controller not ready")
	assert.Contains(t, err.Error(), "cluster dev not registered with slurmdbd yet")
	assert.Empty(t, sacctmgrCalls(t, m.Calls))
}

func TestCreateSlurmAccounts_SacctmgrFails(t *testing.T) {
	var registered atomic.Int32
	var m mock.Executor
	override := accountsOnCall(&registered, mock.Result{Stderr: " Nothing new added.\n", Err: fmt.Errorf("exit status 1")})
	m.OnCall = func(args []string, stdin string) mock.Result {
		r, _ := override(args, stdin)
		return r
	}

	err := createSlurmAccounts(t.Context(), docker.NewClient(&m), mesh.DefaultRealm, accountsCfg(), time.Millisecond, nil)

	require.Error(t, err)
	assert.Equal(t, "creating Slurm accounts: sacctmgr add account physics: exit status 1", err.Error())
	assert.Len(t, sacctmgrCalls(t, m.Calls), 1, "stops at the first failure")
}

func TestCreate_Accounts(t *testing.T) {
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	var registered atomic.Int32
	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), accountsOnCall(&registered, mock.Result{}))
	m.OnStart = pipes.OnStart
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := Create(ctx, client, meshMgr, accountsCfg(), time.Millisecond)
	require.NoError(t, err)

	cmds := sacctmgrCalls(t, m.Calls)
	require.Len(t, cmds, 5)
	// sacctmgr runs after slurmctld is up, and the users exist on the
	// controller by then.
	var enabled, added, first int
	for i, c := range m.Calls {
		joined := strings.Join(c.Args, " ")
		switch {
		case strings.Contains(joined, "sind-dev-controller systemctl enable --now slurmctld"):
			enabled = i
		case c.Args[0] == "exec" && c.Args[1] == "sind-dev-controller" && len(c.Args) == 5 && strings.HasPrefix(c.Args[4], "groupadd "):
			added = i
		case first == 0 && strings.Contains(joined, "sacctmgr -i"):
			first = i
		}
	}
	assert.Less(t, enabled, first)
	assert.Less(t, added, first)
}

func TestCreate_NoAccounts(t *testing.T) {
	// Without accounts, sind runs no sacctmgr, even with a db node.
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), nil)
	m.OnStart = pipes.OnStart
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	cfg := accountsCfg()
	cfg.Accounts = nil
	cfg.Users = nil
	_, err := Create(ctx, client, meshMgr, cfg, time.Millisecond)
	require.NoError(t, err)

	for _, c := range m.Calls {
		assert.NotContains(t, c.Args, "sacctmgr")
	}
}

func TestCreate_AccountsFail(t *testing.T) {
	var registered atomic.Int32
	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), accountsOnCall(&registered, mock.Result{Err: fmt.Errorf("exit status 1")}))
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := Create(ctx, client, meshMgr, accountsCfg(), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating Slurm accounts: sacctmgr add account physics: exit status 1")
}
