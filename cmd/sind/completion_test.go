// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"strings"
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
	m := &mock.Executor{}
	m.AddResult(`[{"Id":"x","Name":"/sind-dev-worker-0","State":{"Status":"running"},"Config":{"Labels":{"sind.role":"worker"}}}]`, "", nil)
	sub := findCmd(completionCtx(m), t, "logs")

	names, directive := sub.ValidArgsFunction(sub, []string{"worker-0.dev"}, "")
	assert.Equal(t, []string{"munge", "sshd", "slurmd"}, names, "the services of the node's role")
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"inspect", "sind-dev-worker-0", "--type", "container"}, m.Calls[0].Args)
}

func TestCompleteLogsArgs_ServiceOfNoSingleNode(t *testing.T) {
	for _, arg := range []string{"worker-[0-1]", "Bad_Name"} {
		t.Run(arg, func(t *testing.T) {
			sub := findCmd(completionCtx(&mock.Executor{}), t, "logs")

			names, directive := sub.ValidArgsFunction(sub, []string{arg}, "")
			assert.Nil(t, names)
			assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
		})
	}
}

func TestCompleteLogsArgs_ServiceError(t *testing.T) {
	m := &mock.Executor{}
	m.AddResult("", "Error: No such object: sind-dev-worker-0", assert.AnError)
	sub := findCmd(completionCtx(m), t, "logs")

	names, directive := sub.ValidArgsFunction(sub, []string{"worker-0.dev"}, "")
	assert.Nil(t, names)
	assert.Equal(t, cobra.ShellCompDirectiveError, directive)
}

func TestCompleteLogsArgs_AfterService(t *testing.T) {
	sub := findCmd(completionCtx(&mock.Executor{}), t, "logs")

	names, directive := sub.ValidArgsFunction(sub, []string{"worker-0.dev", "slurmd"}, "")
	assert.Nil(t, names)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestCompleteNodeName_OneNodeOnly(t *testing.T) {
	sub := findCmd(completionCtx(&mock.Executor{}), t, "get", "node")

	names, directive := sub.ValidArgsFunction(sub, []string{"worker-0"}, "")
	assert.Nil(t, names)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestCompleteNodeName(t *testing.T) {
	m := &mock.Executor{}
	m.AddResult(`{"Names":"sind-dev-worker-0","State":"running","Labels":"sind.realm=sind,sind.cluster=dev,sind.role=worker"}`, "", nil)
	sub := findCmd(completionCtx(m), t, "get", "node")

	names, directive := sub.ValidArgsFunction(sub, nil, "")
	assert.Equal(t, []string{"worker-0.dev"}, names)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestCompleteWorkerNames(t *testing.T) {
	m := &mock.Executor{}
	m.AddResult(
		"{\"Names\":\"sind-dev-worker-0\",\"State\":\"running\",\"Labels\":\"sind.realm=sind,sind.cluster=dev,sind.role=worker\"}\n"+
			"{\"Names\":\"sind-dev-controller\",\"State\":\"running\",\"Labels\":\"sind.realm=sind,sind.cluster=dev,sind.role=controller\"}",
		"", nil)
	sub := findCmd(completionCtx(m), t, "delete", "worker")

	names, directive := sub.ValidArgsFunction(sub, nil, "")
	assert.Equal(t, []string{"worker-0.dev"}, names, "delete worker removes workers only")
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestCompleteContainers(t *testing.T) {
	const (
		kindCP     = `{"Names":"kind-control-plane","State":"running","Networks":"kind,sind-mesh"}`
		kindWorker = `{"Names":"kind-worker","State":"running","Networks":"kind,sind-mesh"}`
		db         = `{"Names":"db","State":"running","Networks":"bridge"}`
		node       = `{"Names":"sind-dev-controller","State":"running","Labels":"sind.realm=sind","Networks":"sind-mesh"}`
	)
	for _, tt := range []struct {
		path []string
		ps   []string // what docker ps lists for the command's filter
		want []string
	}{
		// Every container outside sind not on the mesh yet.
		{[]string{"connect", "container"}, []string{kindCP, kindWorker, db, node}, []string{"db"}},
		// The containers outside sind on the mesh.
		{[]string{"disconnect", "container"}, []string{kindCP, kindWorker, node}, []string{"kind-worker"}},
	} {
		t.Run(tt.path[0], func(t *testing.T) {
			m := &mock.Executor{}
			m.AddResult(strings.Join(tt.ps, "\n"), "", nil)
			sub := findCmd(completionCtx(m), t, tt.path...)

			names, directive := sub.ValidArgsFunction(sub, []string{"kind-control-plane"}, "")
			assert.Equal(t, tt.want, names, "the containers already named are left out")
			assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
		})
	}
}

func TestCompleteContainers_DockerError(t *testing.T) {
	m := &mock.Executor{}
	m.AddResult("", "boom", assert.AnError)
	sub := findCmd(completionCtx(m), t, "connect", "container")

	names, directive := sub.ValidArgsFunction(sub, nil, "")
	assert.Nil(t, names)
	assert.Equal(t, cobra.ShellCompDirectiveError, directive)
}

// TestNoArgsCommandsCompleteNoFiles verifies that the commands that take no
// argument complete nothing, where the shell would offer file names.
func TestNoArgsCommandsCompleteNoFiles(t *testing.T) {
	for _, path := range [][]string{
		{"version"}, {"doctor"}, {"delete", "realm"}, {"get", "clusters"}, {"get", "networks"},
		{"mcp", "start"}, {"mcp", "claude", "list"},
	} {
		t.Run(strings.Join(path, " "), func(t *testing.T) {
			out, _, err := executeCommand(append(append([]string{cobra.ShellCompRequestCmd}, path...), "")...)
			require.NoError(t, err)
			assert.Equal(t, ":4\n", out)
		})
	}
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

func TestCompleteFlagValues(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want []string
	}{
		"get -o":     {[]string{"get", "clusters", "-o", ""}, []string{"human", "json"}},
		"doctor -o":  {[]string{"doctor", "-o", ""}, []string{"human", "json"}},
		"--progress": {[]string{"--progress", ""}, progressModeNames()},
	} {
		t.Run(name, func(t *testing.T) {
			out, _, err := executeCommand(append([]string{cobra.ShellCompRequestCmd}, tc.args...)...)
			require.NoError(t, err)
			for _, w := range tc.want {
				assert.Contains(t, out, w+"\n")
			}
		})
	}
}

func TestCompleteRealmNames(t *testing.T) {
	mock := &mock.Executor{}
	mock.AddResult(
		"{\"Names\":\"sind-dev-worker-0\",\"State\":\"running\",\"Labels\":\"sind.realm=sind,sind.cluster=dev\"}\n"+
			"{\"Names\":\"ci-a-worker-0\",\"State\":\"running\",\"Labels\":\"sind.realm=ci,sind.cluster=a\"}\n", "", nil)
	sub := findCmd(completionCtx(mock), t, "get", "clusters")

	names, directive := completeRealmNames(sub, nil, "")

	assert.ElementsMatch(t, []string{"sind", "ci"}, names)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestCompleteRealmNames_DockerError(t *testing.T) {
	mock := &mock.Executor{}
	mock.AddResult("", "boom", errors.New("exit status 1"))
	sub := findCmd(completionCtx(mock), t, "get", "clusters")

	names, directive := completeRealmNames(sub, nil, "")

	assert.Nil(t, names)
	assert.Equal(t, cobra.ShellCompDirectiveError, directive)
}

func TestCreateClusterCompletesNoExistingNames(t *testing.T) {
	sub := findCmd(completionCtx(&mock.Executor{}), t, "create", "cluster")

	names, directive := sub.ValidArgsFunction(sub, nil, "")

	assert.Empty(t, names)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}
