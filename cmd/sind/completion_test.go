// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func completionCtx(mock *docker.MockExecutor) context.Context {
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
	mock := &docker.MockExecutor{}
	// DiscoverClusterNames calls ListNetworks (NDJSON) then ListVolumes (NDJSON).
	mock.AddResult(
		"{\"Name\":\"sind-dev-net\"}\n{\"Name\":\"sind-prod-net\"}", "", nil)
	mock.AddResult("", "", nil) // no volumes

	sub := findCmd(completionCtx(mock), t, "status")

	names, directive := sub.ValidArgsFunction(sub, nil, "")
	assert.ElementsMatch(t, []string{"dev", "prod"}, names)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestCompleteClusterNames_AlreadyHasArg(t *testing.T) {
	sub := findCmd(completionCtx(&docker.MockExecutor{}), t, "status")

	names, directive := sub.ValidArgsFunction(sub, []string{"existing"}, "")
	assert.Nil(t, names)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestCompleteNodeNames(t *testing.T) {
	mock := &docker.MockExecutor{}
	// DiscoverClusterNames: ListNetworks + ListVolumes
	mock.AddResult("{\"Name\":\"sind-dev-net\"}", "", nil)
	mock.AddResult("", "", nil)
	// GetNodes for "dev": ListContainers (NDJSON)
	mock.AddResult(
		"{\"Names\":\"sind-dev-controller\",\"State\":\"running\",\"Labels\":\"sind.cluster=dev,sind.role=controller\"}\n"+
			"{\"Names\":\"sind-dev-worker-0\",\"State\":\"running\",\"Labels\":\"sind.cluster=dev,sind.role=worker\"}",
		"", nil)

	sub := findCmd(completionCtx(mock), t, "power", "shutdown")

	names, directive := sub.ValidArgsFunction(sub, nil, "")
	assert.ElementsMatch(t, []string{"controller.dev", "worker-0.dev"}, names)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestCompleteLogsArgs_Node(t *testing.T) {
	mock := &docker.MockExecutor{}
	mock.AddResult("{\"Name\":\"sind-dev-net\"}", "", nil)
	mock.AddResult("", "", nil)
	mock.AddResult(
		"{\"Names\":\"sind-dev-controller\",\"State\":\"running\",\"Labels\":\"sind.cluster=dev,sind.role=controller\"}",
		"", nil)

	sub := findCmd(completionCtx(mock), t, "logs")

	names, directive := sub.ValidArgsFunction(sub, nil, "")
	assert.Contains(t, names, "controller.dev")
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestCompleteLogsArgs_Service(t *testing.T) {
	sub := findCmd(completionCtx(&docker.MockExecutor{}), t, "logs")

	names, directive := sub.ValidArgsFunction(sub, []string{"controller.dev"}, "")
	assert.ElementsMatch(t, []string{"slurmctld", "slurmd", "sshd", "munge"}, names)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestCompleteClusterNames_DockerError(t *testing.T) {
	mock := &docker.MockExecutor{}
	mock.AddResult("", "Error", assert.AnError)

	sub := findCmd(completionCtx(mock), t, "status")

	names, directive := sub.ValidArgsFunction(sub, nil, "")
	assert.Nil(t, names)
	assert.Equal(t, cobra.ShellCompDirectiveError, directive)
}
