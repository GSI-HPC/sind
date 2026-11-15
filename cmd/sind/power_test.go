// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/GSI-HPC/go-clikit/cobratree"
	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPower_SubcommandsExist(t *testing.T) {
	subcmds := []string{"shutdown", "cut", "on", "reboot", "cycle", "freeze", "unfreeze"}

	for _, sub := range subcmds {
		t.Run(sub, func(t *testing.T) {
			cmd := NewRootCommand()
			c, _, err := cmd.Find([]string{"power", sub})
			require.NoError(t, err)
			assert.Contains(t, c.Use, "NODES")
		})
	}
}

func TestPower_OffIsShutdown(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"power", "off"})
	require.NoError(t, err)
	assert.Equal(t, "shutdown", c.Name())
}

// TestPower_SuggestsForDockerVerbs verifies that the docker verbs the
// help names suggest the power command that runs them.
func TestPower_SuggestsForDockerVerbs(t *testing.T) {
	for verb, want := range map[string]string{
		"stop": "shutdown", "kill": "cut", "start": "on", "restart": "reboot",
		"pause": "freeze", "unpause": "unfreeze", "resume": "unfreeze",
	} {
		t.Run(verb, func(t *testing.T) {
			_, _, err := executeCommand("power", verb)
			require.EqualError(t, err, `unknown command "`+verb+`" for "sind power" (did you mean "`+want+`"?)`)
		})
	}
}

// TestPower_CompletesOff verifies that the off alias completes once a
// prefix of it is typed, and only then.
func TestPower_CompletesOff(t *testing.T) {
	out, _, err := executeCommand(cobra.ShellCompRequestCmd, "power", "o")
	require.NoError(t, err)
	assert.Contains(t, out, "off\tGracefully shut down nodes (docker stop; alias: off)\n")
	assert.Contains(t, out, "on\t")

	out, _, err = executeCommand(cobra.ShellCompRequestCmd, "power", "")
	require.NoError(t, err)
	assert.NotContains(t, out, "off\t")
	assert.Contains(t, out, "shutdown\t")

	out, _, err = executeCommand(cobra.ShellCompRequestCmd, "power", "x")
	require.NoError(t, err)
	assert.Equal(t, ":4\n", out)
}

func TestPower_RequiresArgs(t *testing.T) {
	subcmds := []string{"shutdown", "cut", "on", "reboot", "cycle", "freeze", "unfreeze"}

	for _, sub := range subcmds {
		t.Run(sub, func(t *testing.T) {
			_, _, err := executeCommand("power", sub)
			assert.Error(t, err)
		})
	}
}

// TestPower_WaitFlag checks that the commands that start nodes take
// --wait, and the others do not.
func TestPower_WaitFlag(t *testing.T) {
	for sub, want := range map[string]bool{
		"on": true, "reboot": true, "cycle": true, "unfreeze": true,
		"shutdown": false, "cut": false, "freeze": false,
	} {
		c, _, err := NewRootCommand().Find([]string{"power", sub})
		require.NoError(t, err)
		assert.Equal(t, want, c.Flags().Lookup("wait") != nil, sub)
	}
}

// unfreezeOnCall answers power unfreeze of worker-0 in cluster dev, and
// the calls of its wait: the node is ready, and slurmctld lists it in
// state.
func unfreezeOnCall(t *testing.T, state string) func([]string, string) mock.Result {
	return func(args []string, _ string) mock.Result {
		joined := strings.Join(args, " ")
		switch {
		case args[0] == "ps":
			return mock.Result{Stdout: testutil.NDJSON(
				testutil.PsEntry{ID: "c1", Names: "sind-dev-controller", State: "running", Image: "img", Labels: "sind.cluster=dev,sind.role=controller"},
				testutil.PsEntry{ID: "c2", Names: "sind-dev-worker-0", State: "running", Image: "img", Labels: "sind.cluster=dev,sind.role=worker"},
			)}
		case args[0] == "unpause":
			return mock.Result{}
		case args[0] == "inspect":
			return mock.Result{Stdout: `[{"Id":"id","Name":"/` + args[1] + `","State":{"Status":"running"},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{}}}]`}
		case strings.Contains(joined, "is-system-running"):
			return mock.Result{Stdout: "running\n"}
		case strings.Contains(joined, "/dev/tcp"):
			return mock.Result{Stdout: "SSH-2.0-OpenSSH_9.0\n"}
		case args[0] == "exec" && args[2] == "systemctl":
			return mock.Result{Stdout: "active\n"}
		case args[0] == "exec" && args[1] == "sind-dev-controller" && args[2] == "sinfo":
			return mock.Result{Stdout: "worker-0|" + state + "|0/2/0/2|2048|all*|none\n"}
		}
		t.Errorf("unexpected call: %v", args)
		return mock.Result{Err: errors.New("unexpected call")}
	}
}

// sinfoCalls counts the sinfo calls of m: the waits for registration.
func sinfoCalls(m *mock.Executor) int {
	n := 0
	for _, c := range m.Calls {
		if len(c.Args) > 2 && c.Args[0] == "exec" && c.Args[2] == "sinfo" {
			n++
		}
	}
	return n
}

func TestPower_Wait(t *testing.T) {
	t.Setenv("SIND_WAIT", "")
	m := &mock.Executor{OnCall: unfreezeOnCall(t, "idle")}
	_, _, err := executeWithMock(m, "power", "unfreeze", "worker-0.dev", "--wait", "1m")
	require.NoError(t, err)
	assert.Equal(t, 1, sinfoCalls(m), "the wait asks slurmctld")

	m = &mock.Executor{OnCall: unfreezeOnCall(t, "idle")}
	_, _, err = executeWithMock(m, "power", "unfreeze", "worker-0.dev")
	require.NoError(t, err)
	assert.Zero(t, sinfoCalls(m), "no wait without --wait")
}

func TestPower_WaitFromEnv(t *testing.T) {
	t.Setenv("SIND_WAIT", "100ms")
	m := &mock.Executor{OnCall: unfreezeOnCall(t, "idle*")}
	_, _, err := executeWithMock(m, "power", "unfreeze", "worker-0.dev")
	require.ErrorIs(t, err, cluster.ErrNotReady)
	assert.ErrorContains(t, err, `nodes of cluster "dev" not ready within 100ms`)
}

func TestPower_WaitInvalid(t *testing.T) {
	t.Setenv("SIND_WAIT", "")
	var m mock.Executor
	_, _, err := executeWithMock(&m, "power", "unfreeze", "worker-0.dev", "--wait=-1s")
	require.EqualError(t, err, "--wait must not be negative, got -1s")
	assert.True(t, cobratree.IsUsage(err))
	assert.Empty(t, m.Calls)

	t.Setenv("SIND_WAIT", "soon")
	_, _, err = executeWithMock(&m, "power", "unfreeze", "worker-0.dev")
	require.EqualError(t, err, `invalid SIND_WAIT value "soon": want a duration with a unit, such as 90s, 5m or 1h30m`)
	assert.Empty(t, m.Calls)
}

// --- Integration ---

func TestPowerLifecycle(t *testing.T) {
	t.Parallel()
	c := realClient(t)
	ctx := t.Context()
	realm := "it-pwr-" + testID
	cluster := "cli-pwr-" + testID

	ctrName := docker.ContainerName(realm + "-" + cluster + "-worker-0")

	t.Cleanup(func() {
		bg := context.Background()
		_ = c.KillContainer(bg, ctrName)
		_ = c.RemoveContainer(bg, ctrName)
	})

	_, err := c.RunContainer(ctx,
		"--name", string(ctrName),
		"--label", "sind.realm="+realm,
		"--label", "sind.cluster="+cluster,
		"--label", "sind.role=worker",
		"busybox:latest", "sleep", "120",
	)
	require.NoError(t, err)

	node := "worker-0." + cluster

	// shutdown (stop)
	_, _, err = executeWithRealm(realm, "power", "shutdown", node)
	require.NoError(t, err)
	info, err := c.InspectContainer(ctx, ctrName)
	require.NoError(t, err)
	assert.Equal(t, docker.StateExited, info.Status)

	// on (start)
	_, _, err = executeWithRealm(realm, "power", "on", node)
	require.NoError(t, err)
	info, err = c.InspectContainer(ctx, ctrName)
	require.NoError(t, err)
	assert.Equal(t, docker.StateRunning, info.Status)

	// freeze (pause)
	_, _, err = executeWithRealm(realm, "power", "freeze", node)
	require.NoError(t, err)
	info, err = c.InspectContainer(ctx, ctrName)
	require.NoError(t, err)
	assert.Equal(t, docker.StatePaused, info.Status)

	// unfreeze (unpause)
	_, _, err = executeWithRealm(realm, "power", "unfreeze", node)
	require.NoError(t, err)
	info, err = c.InspectContainer(ctx, ctrName)
	require.NoError(t, err)
	assert.Equal(t, docker.StateRunning, info.Status)

	// reboot (stop + start)
	_, _, err = executeWithRealm(realm, "power", "reboot", node)
	require.NoError(t, err)
	info, err = c.InspectContainer(ctx, ctrName)
	require.NoError(t, err)
	assert.Equal(t, docker.StateRunning, info.Status)

	// cycle (kill + start)
	_, _, err = executeWithRealm(realm, "power", "cycle", node)
	require.NoError(t, err)
	info, err = c.InspectContainer(ctx, ctrName)
	require.NoError(t, err)
	assert.Equal(t, docker.StateRunning, info.Status)

	// cut (kill)
	_, _, err = executeWithRealm(realm, "power", "cut", node)
	require.NoError(t, err)
	info, err = c.InspectContainer(ctx, ctrName)
	require.NoError(t, err)
	assert.NotEqual(t, "running", info.Status)
}
