// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func completionCtx(mock *mock.Executor) context.Context {
	client := docker.NewClient(mock)
	return withClient(context.Background(), client)
}

func findCmd(ctx context.Context, t *testing.T, path ...string) *cobra.Command {
	t.Helper()
	cmd := NewRootCommand()
	sub, _, err := cmd.Find(path)
	require.NoError(t, err)
	sub.SetContext(ctx)
	return sub
}

func TestCompleteClusterNames(t *testing.T) {
	// DiscoverClusterNames runs ListNetworks and ListVolumes concurrently.
	mock := &mock.Executor{OnCall: func(args []string, _ string) mock.Result {
		if args[0] == "network" {
			return mock.Result{Stdout: `{"Name":"sind-dev-net","Labels":"sind.realm=sind,sind.cluster=dev"}` + "\n" +
				`{"Name":"sind-prod-net","Labels":"sind.realm=sind,sind.cluster=prod"}`}
		}
		return mock.Result{} // no volumes
	}}

	sub := findCmd(completionCtx(mock), t, "get", "cluster")

	names, directive := sub.ValidArgsFunction(sub, nil, "")
	assert.ElementsMatch(t, []string{"dev", "prod"}, names)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestCompleteClusterNames_AlreadyHasArg(t *testing.T) {
	sub := findCmd(completionCtx(&mock.Executor{}), t, "get", "cluster")

	names, directive := sub.ValidArgsFunction(sub, []string{"existing"}, "")
	assert.Nil(t, names)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestCompleteNodeNames(t *testing.T) {
	mock := &mock.Executor{}
	// One realm-wide ListContainers (NDJSON), no inspect.
	mock.AddResult(
		"{\"Names\":\"sind-dev-worker-0\",\"State\":\"running\",\"Labels\":\"sind.realm=sind,sind.cluster=dev,sind.role=worker\"}\n"+
			"{\"Names\":\"sind-dns\",\"State\":\"running\",\"Labels\":\"sind.realm=sind\"}\n"+
			"{\"Names\":\"sind-dev-controller\",\"State\":\"running\",\"Labels\":\"sind.realm=sind,sind.cluster=dev,sind.role=controller\"}",
		"", nil)

	sub := findCmd(completionCtx(mock), t, "power", "shutdown")

	names, directive := sub.ValidArgsFunction(sub, nil, "")
	assert.Equal(t, []string{"controller.dev", "worker-0.dev"}, names)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
	require.Len(t, mock.Calls, 1)
	assert.Equal(t, []string{"ps", "-a", "--no-trunc", "--format", "json", "--filter", "label=sind.realm=sind"}, mock.Calls[0].Args)
}

func TestCompleteNodeNames_DockerError(t *testing.T) {
	mock := &mock.Executor{}
	mock.AddResult("", "Error", assert.AnError)

	sub := findCmd(completionCtx(mock), t, "power", "shutdown")

	names, directive := sub.ValidArgsFunction(sub, nil, "")
	assert.Nil(t, names)
	assert.Equal(t, cobra.ShellCompDirectiveError, directive)
}

func TestCompleteLogsArgs_Node(t *testing.T) {
	mock := &mock.Executor{}
	mock.AddResult(
		"{\"Names\":\"sind-dev-controller\",\"State\":\"running\",\"Labels\":\"sind.cluster=dev,sind.role=controller\"}",
		"", nil)

	sub := findCmd(completionCtx(mock), t, "logs")

	names, directive := sub.ValidArgsFunction(sub, nil, "")
	assert.Contains(t, names, "controller.dev")
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestCompleteLogsArgs_Service(t *testing.T) {
	sub := findCmd(completionCtx(&mock.Executor{}), t, "logs")

	names, directive := sub.ValidArgsFunction(sub, []string{"controller.dev"}, "")
	assert.ElementsMatch(t, []string{"slurmctld", "slurmd", "slurmdbd", "mariadb", "sackd", "sshd", "munge"}, names)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestCompleteSSHNodeArg_EmptyArgs(t *testing.T) {
	mock := &mock.Executor{}
	mock.AddResult(
		"{\"Names\":\"sind-dev-controller\",\"State\":\"running\",\"Labels\":\"sind.cluster=dev,sind.role=controller\"}",
		"", nil)

	sub := findCmd(completionCtx(mock), t, "ssh")

	names, directive := completeSSHNodeArg(sub, nil, "")
	assert.Contains(t, names, "controller.dev")
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestCompleteSSHNodeArg_SkipsSSHFlags(t *testing.T) {
	mock := &mock.Executor{}
	mock.AddResult(
		"{\"Names\":\"sind-dev-controller\",\"State\":\"running\",\"Labels\":\"sind.cluster=dev,sind.role=controller\"}",
		"", nil)

	sub := findCmd(completionCtx(mock), t, "ssh")

	names, directive := completeSSHNodeArg(sub, []string{"-v", "-L", "8080:localhost:80"}, "")
	assert.Contains(t, names, "controller.dev")
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestCompleteSSHNodeArg_AfterFlagValue(t *testing.T) {
	sub := findCmd(completionCtx(&mock.Executor{}), t, "ssh")

	// -o expects a value — should not offer node completions
	names, directive := completeSSHNodeArg(sub, []string{"-o"}, "Strict")
	assert.Nil(t, names)
	assert.Equal(t, cobra.ShellCompDirectiveDefault, directive)
}

func TestCompleteSSHNodeArg_NodeAlreadyProvided(t *testing.T) {
	sub := findCmd(completionCtx(&mock.Executor{}), t, "ssh")

	names, directive := completeSSHNodeArg(sub, []string{"controller.dev"}, "")
	assert.Nil(t, names)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestCompleteSSHNodeArg_AfterDashDash(t *testing.T) {
	sub := findCmd(completionCtx(&mock.Executor{}), t, "ssh")

	names, directive := completeSSHNodeArg(sub, []string{"controller.dev", "--"}, "")
	assert.Nil(t, names)
	assert.Equal(t, cobra.ShellCompDirectiveDefault, directive)
}

func TestCompleteSSHNodeArg_DashPrefix(t *testing.T) {
	sub := findCmd(completionCtx(&mock.Executor{}), t, "ssh")

	names, directive := completeSSHNodeArg(sub, nil, "-")
	assert.Nil(t, names)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestCompleteExecClusterArg_EmptyArgs(t *testing.T) {
	mock := &mock.Executor{OnCall: func(args []string, _ string) mock.Result {
		if args[0] == "network" {
			return mock.Result{Stdout: `{"Name":"sind-dev-net","Labels":"sind.realm=sind,sind.cluster=dev"}`}
		}
		return mock.Result{} // no volumes
	}}

	sub := findCmd(completionCtx(mock), t, "exec")

	names, directive := completeExecClusterArg(sub, nil, "")
	assert.ElementsMatch(t, []string{"dev"}, names)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestCompleteExecClusterArg_ClusterAlreadyProvided(t *testing.T) {
	sub := findCmd(completionCtx(&mock.Executor{}), t, "exec")

	names, directive := completeExecClusterArg(sub, []string{"dev"}, "")
	assert.Nil(t, names)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestCompleteExecClusterArg_AfterDashDash(t *testing.T) {
	sub := findCmd(completionCtx(&mock.Executor{}), t, "exec")

	names, directive := completeExecClusterArg(sub, []string{"dev", "--"}, "")
	assert.Nil(t, names)
	assert.Equal(t, cobra.ShellCompDirectiveDefault, directive)
}

func TestCompleteClusterNames_DockerError(t *testing.T) {
	mock := &mock.Executor{OnCall: func([]string, string) mock.Result {
		return mock.Result{Stderr: "Error", Err: assert.AnError}
	}}

	sub := findCmd(completionCtx(mock), t, "get", "cluster")

	names, directive := sub.ValidArgsFunction(sub, nil, "")
	assert.Nil(t, names)
	assert.Equal(t, cobra.ShellCompDirectiveError, directive)
}
