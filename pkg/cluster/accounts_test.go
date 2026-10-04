// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

// wantAccountCommands are the sacctmgr commands of accountsCfg.
var wantAccountCommands = [][]string{
	{"add", "account", "physics"},
	{"add", "account", "theory", "parent=physics"},
	{"add", "user", "alice", "account=theory", "defaultaccount=theory"},
	{"add", "user", "bob", "account=physics,theory", "defaultaccount=physics"},
	{"add", "coordinator", "account=physics", "names=bob"},
}

// isAccountsScript reports whether args run the account commands' script.
func isAccountsScript(args []string) bool {
	return len(args) == 5 && args[0] == "exec" && args[2] == "sh" && args[3] == "-ec" &&
		strings.HasPrefix(args[4], accountsRun)
}

// accountsOnCall answers the sacctmgr calls of createSlurmAccounts: show
// cluster lists the cluster once registered reaches zero, counting down
// with each call, and the account commands' script returns result.
func accountsOnCall(registered *atomic.Int32, result mock.Result) func(args []string, _ string) (mock.Result, bool) {
	return func(args []string, _ string) (mock.Result, bool) {
		if isAccountsScript(args) {
			return result, true
		}
		if args[0] != "exec" || len(args) < 3 || args[2] != "sacctmgr" {
			return mock.Result{}, false
		}
		if registered.Add(-1) >= 0 {
			return mock.Result{}, true
		}
		return mock.Result{Stdout: "dev\n"}, true
	}
}

// accountsScripts returns the scripts of the account commands' execs.
// They all run on the controller.
func accountsScripts(t *testing.T, calls []mock.Call) []string {
	t.Helper()
	var out []string
	for _, c := range calls {
		if isAccountsScript(c.Args) {
			assert.Equal(t, "sind-dev-controller", c.Args[1])
			out = append(out, c.Args[4])
		}
	}
	return out
}

// runAccountsScript runs script with sh and a stub sacctmgr that records
// each call's arguments, one per line, and fails for a call that names
// failFor after "-i add KIND". It returns the recorded calls, the script's
// stderr and its error.
func runAccountsScript(t *testing.T, script, failFor string) ([][]string, string, error) {
	t.Helper()
	dir := t.TempDir()
	record := filepath.Join(dir, "calls")
	stub := `#!/bin/sh
for a in "$@"; do printf '%s\n' "$a"; done >> "$RECORD"
printf -- '--\n' >> "$RECORD"
if [ "$4" = "$FAIL_FOR" ]; then echo " Nothing new added."; exit 1; fi
echo " Adding Account(s)"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sacctmgr"), []byte(stub), 0o755))

	cmd := exec.Command("sh", "-ec", script)
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "RECORD="+record, "FAIL_FOR="+failFor)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	data, err := os.ReadFile(record)
	require.NoError(t, err)
	var calls [][]string
	var call []string
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSuffix(line, "\n")
		if line == "--" {
			calls = append(calls, call)
			call = nil
			continue
		}
		call = append(call, line)
	}
	return calls, stderr.String(), runErr
}

func TestAccountsScript_RunsEachCommand(t *testing.T) {
	cmds := append(slices.Clone(wantAccountCommands),
		[]string{"add", "account", "odd", "Description=it's $HOME `x` \"q\" \\ ;"})
	calls, stderr, err := runAccountsScript(t, accountsScript(cmds), "")
	require.NoError(t, err, stderr)
	want := make([][]string, len(cmds))
	for i, cmd := range cmds {
		want[i] = append([]string{"-i"}, cmd...)
	}
	assert.Equal(t, want, calls)
}

func TestAccountsScript_StopsAtFirstFailure(t *testing.T) {
	calls, stderr, err := runAccountsScript(t, accountsScript(wantAccountCommands), "theory")
	require.Error(t, err)
	assert.Equal(t, "sacctmgr add account theory parent=physics:  Nothing new added.\n", stderr)
	assert.Len(t, calls, 2, "stops at the first failure")
}

func TestShellQuote(t *testing.T) {
	assert.Equal(t, `'plain'`, shellQuote("plain"))
	assert.Equal(t, `'it'\''s'`, shellQuote("it's"))
	assert.Equal(t, `''`, shellQuote(""))
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
	// Three registration checks, then one exec for the five commands.
	require.Len(t, m.Calls, 4)
	for _, c := range m.Calls[:3] {
		assert.Equal(t, []string{"exec", "sind-dev-controller", "sacctmgr", "-n", "-P", "show", "cluster", "format=cluster"}, c.Args)
	}
	assert.Equal(t, []string{accountsScript(wantAccountCommands)}, accountsScripts(t, m.Calls))
}

func TestCreateSlurmAccounts_NoCommands(t *testing.T) {
	var registered atomic.Int32
	var m mock.Executor
	override := accountsOnCall(&registered, mock.Result{})
	m.OnCall = func(args []string, stdin string) mock.Result {
		r, _ := override(args, stdin)
		return r
	}
	cfg := accountsCfg()
	cfg.Accounts, cfg.Users = nil, nil

	require.NoError(t, createSlurmAccounts(t.Context(), docker.NewClient(&m), mesh.DefaultRealm, cfg, time.Millisecond, nil))
	assert.Empty(t, accountsScripts(t, m.Calls))
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
	assert.Empty(t, accountsScripts(t, m.Calls))
}

func TestCreateSlurmAccounts_SacctmgrFails(t *testing.T) {
	var registered atomic.Int32
	var m mock.Executor
	override := accountsOnCall(&registered, mock.Result{
		Stderr: "sacctmgr add account physics:  Nothing new added.\n",
		Err:    fmt.Errorf("exit status 1"),
	})
	m.OnCall = func(args []string, stdin string) mock.Result {
		r, _ := override(args, stdin)
		return r
	}

	err := createSlurmAccounts(t.Context(), docker.NewClient(&m), mesh.DefaultRealm, accountsCfg(), time.Millisecond, nil)

	require.Error(t, err)
	assert.Equal(t, "creating Slurm accounts: exit status 1", err.Error())
	assert.Len(t, accountsScripts(t, m.Calls), 1)
}

// hangingScript runs the account commands' script until its context ends,
// as a sacctmgr that loops forever would.
type hangingScript struct {
	*mock.Executor
}

func (h hangingScript) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	if isAccountsScript(args) {
		<-ctx.Done()
		return "", "", fmt.Errorf("signal: killed")
	}
	return h.Executor.Run(ctx, name, args...)
}

func (h hangingScript) RunWithStdin(ctx context.Context, stdin io.Reader, name string, args ...string) (string, string, error) {
	return h.Executor.RunWithStdin(ctx, stdin, name, args...)
}

func TestCreateSlurmAccounts_Timeout(t *testing.T) {
	saved := sacctmgrTimeout
	sacctmgrTimeout = 20 * time.Millisecond
	t.Cleanup(func() { sacctmgrTimeout = saved })

	var registered atomic.Int32
	m := &mock.Executor{}
	override := accountsOnCall(&registered, mock.Result{})
	m.OnCall = func(args []string, stdin string) mock.Result {
		r, _ := override(args, stdin)
		return r
	}

	err := createSlurmAccounts(t.Context(), docker.NewClient(hangingScript{m}), mesh.DefaultRealm, accountsCfg(), time.Millisecond, nil)

	require.EqualError(t, err, "creating Slurm accounts: sacctmgr did not finish within 20ms")
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

	require.Equal(t, []string{accountsScript(wantAccountCommands)}, accountsScripts(t, m.Calls))
	// sacctmgr runs after slurmctld is up, and the users exist on the
	// controller by then.
	var enabled, added, first int
	for i, c := range m.Calls {
		joined := strings.Join(c.Args, " ")
		switch {
		case strings.Contains(joined, "sind-dev-controller systemctl enable --now slurmctld"):
			enabled = i
		case isUsersSetup(c.Args, "sind-dev-controller"):
			added = i
		case isAccountsScript(c.Args):
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
		assert.False(t, isAccountsScript(c.Args))
	}
}

func TestCreate_AccountsFail(t *testing.T) {
	var registered atomic.Int32
	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), accountsOnCall(&registered, mock.Result{
		Stderr: "sacctmgr add account physics:  Nothing new added.\n",
		Err:    fmt.Errorf("exit status 1"),
	}))
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := Create(ctx, client, meshMgr, accountsCfg(), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating Slurm accounts: exit status 1")
}
